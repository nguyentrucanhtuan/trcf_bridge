// Package trcfcore — lõi cầu nối đóng gói cho Android qua `gomobile bind`.
//
// Đây là lớp vỏ MỎNG quanh `internal/core`: không có logic in nào ở đây. Nó chỉ đổi
// kiểu dữ liệu sang dạng gomobile chuyển được (chuỗi, []byte, số, interface) và cắm
// đường USB do Kotlin cài đặt vào `printer.SetUSBBackend`. Mọi luật an toàn (dò lại
// máy trước mỗi lần in, 0 khớp thì không ghi bừa, hai mốc chờ 15s + 45s, cờ
// `Delivered`, hàng đợi theo thiết bị) vẫn là code Go dùng chung với Linux.
//
// Là module Go RIÊNG để module gốc giữ nguyên "chỉ thư viện chuẩn" — build Linux và
// Dockerfile không phải tải gì từ mạng.
package trcfcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/core"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/escpos"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

// UsbHost — đường USB phía Android (Kotlin cài đặt bằng `UsbManager`).
type UsbHost interface {
	// ListJSON trả mảng JSON các máy in đang cắm VÀ đã được cấp quyền:
	// [{"path","mfg","model","cmd","serial","ids","usb_path"}]. `path` là thứ `Open`
	// nhận lại (`UsbDevice.deviceName`).
	ListJSON() (string, error)
	Open(path string) (UsbConn, error)
}

// UsbConn — một luồng ghi CHẶN tới máy in. `Write` chỉ trả về khi byte đã đi ra cổng
// và trả ĐÚNG số byte đã đi: lõi dựa vào con số đó để quyết ERP có được in lại hay
// không.
//
// Phía Java lỗi là EXCEPTION, và ném exception thì số byte mất (Go nhận n = 0). Nên
// hợp đồng là: CHỈ ném khi lần gọi này chưa đẩy được byte nào; đã đẩy được một phần
// thì TRẢ VỀ số đó, không ném. Lõi sẽ gọi lại với phần còn thiếu, lần đó hỏng ngay ở
// byte đầu và mới ném — lúc ấy lõi đã đếm phần đã đi nên báo ERP "có thể đã in".
type UsbConn interface {
	Write(b []byte) (int, error)
	Close() error
}

// Logger nhận từng dòng nhật ký (Kotlin chuyển sang Logcat + màn trạng thái).
type Logger interface {
	Log(line string)
}

// SetUsbHost cắm đường USB của Android. Gọi một lần, trước mọi hàm khác.
func SetUsbHost(h UsbHost) {
	if h == nil {
		printer.SetUSBBackend(nil)
		return
	}
	printer.SetUSBBackend(&usbBackend{host: h})
}

// SetTimezone đặt múi giờ cho nhật ký. Go trên Android không đọc được múi giờ máy
// (không có /etc/localtime) nên mặc định là UTC — nhật ký lệch 7 tiếng so với đồng
// hồ ở quầy, người đọc lần theo sự cố sẽ tra nhầm giờ. `name` là tên IANA
// (vd "Asia/Ho_Chi_Minh"); không nạp được thì dùng độ lệch cố định `offsetSeconds`.
func SetTimezone(name string, offsetSeconds int) {
	if loc, err := time.LoadLocation(name); err == nil {
		time.Local = loc
		return
	}
	time.Local = time.FixedZone(name, offsetSeconds)
}

// SetLogger thay nơi ghi nhật ký của lõi.
func SetLogger(l Logger) {
	f := func(format string, a ...any) {
		line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, a...)
		if l != nil {
			l.Log(line)
		}
	}
	core.Logf = f
	printer.Logf = f
}

// ─────────────────────────────────────────────────────────────────────────────
// Máy in đang cắm
// ─────────────────────────────────────────────────────────────────────────────

// printerJSON — một máy in cho màn thiết lập chọn.
type printerJSON struct {
	Key    string `json:"key"`    // danh tính bền vững, dùng để chọn máy khi in thử/ghép
	Label  string `json:"label"`  // cho người đọc
	Path   string `json:"path"`   // chỗ nó nằm hôm nay
	Family string `json:"family"` // "escpos" | "tspl" | "" (không rõ)
}

// ListPrintersJSON — máy in đang cắm, cho màn thiết lập. Máy không khai danh tính
// nào thì bỏ: ghép vào một máy không phân giải lại được thì lệnh in nào cũng hỏng.
func ListPrintersJSON() (string, error) {
	found, err := printer.Discover()
	if err != nil {
		return "", err
	}
	out := make([]printerJSON, 0, len(found))
	for _, f := range found {
		if f.Identity.IsZero() {
			continue
		}
		out = append(out, printerJSON{
			Key:    f.Identity.Key(),
			Label:  f.Identity.Label(),
			Path:   f.Path,
			Family: printer.CommandFamily(f.Identity.CMD),
		})
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// targetFor dựng cấu hình máy in từ khoá danh tính màn thiết lập đã chọn.
func targetFor(key string) (printer.Config, error) {
	want, err := printer.ParseIdentity(key)
	if err != nil {
		return printer.Config{}, err
	}
	found, err := printer.Discover()
	if err != nil {
		return printer.Config{}, err
	}
	res, err := printer.Resolve(want, found)
	if err != nil {
		return printer.Config{}, err
	}
	id := res.Device.Identity
	return printer.Config{Kind: printer.KindUSB, Target: res.Device.Path, Identity: &id}, nil
}

// TestPrint in trang thử (kèm đá két nếu `drawer`) ra máy có khoá `key`. Không cần ERP.
func TestPrint(key, version string, drawer bool, drawerPin int) error {
	target, err := targetFor(key)
	if err != nil {
		return err
	}
	p, err := printer.Open(target)
	if err != nil {
		return err
	}
	payload := escpos.TestPage(version, target.Target)
	if drawer {
		payload = append(payload, escpos.KickDrawer(drawerPin)...)
	}
	return p.Write(payload)
}

// ─────────────────────────────────────────────────────────────────────────────
// Ghép nối
// ─────────────────────────────────────────────────────────────────────────────

// Pair in thử rồi ghép máy in có khoá `key` với ERP (xem `core.Pair` cho thứ tự an
// toàn). Trả tên máy in bên ERP để người lắp xác nhận ghép đúng máy.
func Pair(cfgPath, server, code, key, hostname, version string) (string, error) {
	target, err := targetFor(key)
	if err != nil {
		return "", err
	}
	res, _, err := core.Pair(context.Background(), core.PairParams{
		ConfigPath: cfgPath,
		Server:     server,
		Code:       code,
		Target:     target,
		Hostname:   hostname,
		OS:         "android",
		Version:    version,
	}, func(line string) { core.Logf("%s", line) })
	if err != nil {
		return "", err
	}
	return res.PrinterName, nil
}

// IsPaired — máy này đã có cấu hình chạy được chưa.
func IsPaired(cfgPath string) bool {
	_, err := config.Load(cfgPath)
	return err == nil
}

// ServerURL — địa chỉ ERP trong cấu hình (rỗng nếu chưa ghép). App dùng chính địa chỉ
// này để mở POS web, nên người lắp chỉ nhập địa chỉ đúng MỘT lần.
func ServerURL(cfgPath string) string {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return ""
	}
	return cfg.ServerURL
}

// ─────────────────────────────────────────────────────────────────────────────
// Chạy nền
// ─────────────────────────────────────────────────────────────────────────────

var (
	runMu     sync.Mutex
	runCancel context.CancelFunc
	runDone   chan struct{}
)

// retryAfter — `core.Run` thoát vì lỗi (chưa mở được máy in nào, cấu hình hỏng) thì
// thử lại sau chừng này. Linux có systemd `Restart=` lo việc đó; Android thì không.
const retryAfter = 30 * time.Second

// Start chạy lõi trong nền và trả về NGAY. Đang chạy rồi thì không làm gì.
func Start(cfgPath, version string) error {
	runMu.Lock()
	defer runMu.Unlock()
	if runCancel != nil {
		return nil
	}
	if _, err := config.Load(cfgPath); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	runCancel, runDone = cancel, done

	go func() {
		defer close(done)
		for {
			err := core.Run(ctx, cfgPath, version)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				err = errors.New("vòng chạy dừng bất thường")
			}
			core.Logf("⚠ %v — thử lại sau %s", err, retryAfter)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryAfter):
			}
		}
	}()
	return nil
}

// Stop yêu cầu lõi dừng và chờ tối đa `waitMillis`. Lõi luôn in nốt lệnh đang dở
// trước khi thoát (cắt ngang = ERP in lại tờ thứ hai), nên trả `false` nghĩa là còn
// đang in nốt — không phải lỗi.
func Stop(waitMillis int) bool {
	runMu.Lock()
	cancel, done := runCancel, runDone
	runCancel, runDone = nil, nil
	runMu.Unlock()
	if cancel == nil {
		return true
	}
	cancel()
	select {
	case <-done:
		return true
	case <-time.After(time.Duration(waitMillis) * time.Millisecond):
		return false
	}
}

// Running — lõi có đang chạy không.
func Running() bool {
	runMu.Lock()
	defer runMu.Unlock()
	return runCancel != nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Cầu USB: Kotlin → printer.USBBackend
// ─────────────────────────────────────────────────────────────────────────────

type usbBackend struct{ host UsbHost }

type deviceJSON struct {
	Path    string `json:"path"`
	Mfg     string `json:"mfg"`
	Model   string `json:"model"`
	CMD     string `json:"cmd"`
	Serial  string `json:"serial"`
	IDs     string `json:"ids"`
	USBPath string `json:"usb_path"`
}

func (b *usbBackend) Discover() ([]printer.Found, error) {
	raw, err := b.host.ListJSON()
	if err != nil {
		return nil, err
	}
	var devs []deviceJSON
	if err := json.Unmarshal([]byte(raw), &devs); err != nil {
		return nil, fmt.Errorf("danh sách máy in từ Android không đọc được: %w", err)
	}
	out := make([]printer.Found, 0, len(devs))
	for _, d := range devs {
		out = append(out, printer.Found{
			Path: d.Path,
			Identity: printer.Identity{
				Serial: d.Serial, CMD: d.CMD, IDs: d.IDs,
				USBPath: d.USBPath, Model: d.Model, Mfg: d.Mfg,
			},
		})
	}
	return out, nil
}

func (b *usbBackend) Open(path string) (io.WriteCloser, error) {
	c, err := b.host.Open(path)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("không mở được máy in %s", path)
	}
	return conn{c}, nil
}

// conn đổi UsbConn (gomobile) thành io.WriteCloser.
type conn struct{ c UsbConn }

func (w conn) Write(p []byte) (int, error) { return w.c.Write(p) }
func (w conn) Close() error                { return w.c.Close() }
