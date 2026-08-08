// Package transport nói chuyện với ERP.
//
// Agent LUÔN gọi ra, ERP không bao giờ gọi vào (PRD FR-047.8). Nhờ vậy máy quầy
// không phải mở cổng, không cần IP tĩnh, không vướng NAT/firewall — và khi
// backend dời lên cloud thì không phải đổi gì ở quán.
package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PollWait — thời gian ERP giữ request khi chưa có việc. 25s: đủ dài để không
// nện request liên tục, đủ ngắn để lọt dưới mọi hạn nghỉ mặc định (proxy, LB,
// Caddy) vốn thường là 30–60s.
const PollWait = 25 * time.Second

// pollTimeout để dư 10s so với PollWait: server đáng ra trả lời ở giây 25, dư
// ra là để chịu độ trễ mạng — hết 35s mà im thì coi như đứt thật.
const pollTimeout = PollWait + 10*time.Second

// Job — một lần in.
type Job struct {
	ID int64 `json:"job_id"`
	// PrinterID — máy in nào phải nhận. Máy quầy cầm nhiều máy in nên đây là thứ
	// duy nhất phân biệt tờ bill với phiếu bếp.
	PrinterID int    `json:"printer_id"`
	Format    string `json:"format"` // hiện chỉ "escpos"
	Data      string `json:"data"`   // base64 của byte ĐÃ DỰNG SẴN ở backend
}

// Payload giải mã data. Agent chỉ giải base64 — KHÔNG diễn giải nội dung.
func (j Job) Payload() ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(j.Data)
	if err != nil {
		return nil, fmt.Errorf("job %d: dữ liệu in hỏng (base64): %w", j.ID, err)
	}
	return b, nil
}

// PrinterStatus — trạng thái cổng của một máy in, gửi kèm heartbeat.
type PrinterStatus struct {
	PrinterID int    `json:"printer_id"`
	PortOK    bool   `json:"port_ok"`
	LastErr   string `json:"last_err,omitempty"`

	// DeviceKey — chiếc máy agent ĐANG THỰC SỰ dùng cho máy in này (047f).
	//
	// Để ERP tự chữa bản gán đời cũ: quán nâng cấp xong thì `device_key` bên ERP vẫn
	// là "/dev/usb/lp4" của lần ghép cũ, không khớp khoá nào trong kiểm kê, nên màn
	// Máy in sẽ treo dòng đỏ "không thấy thiết bị đã gán" trên một máy in đang chạy
	// tốt — báo động giả, và người dùng phải bấm chọn lại dù chẳng có gì hỏng.
	//
	// Chỉ gửi khi đã phân giải ĐƯỢC. Máy in rút dây thì bỏ trống, để ERP giữ nguyên
	// khoá cũ và dòng đỏ kia trở thành cảnh báo THẬT.
	DeviceKey string `json:"device_key,omitempty"`
}

// Client — một agent (một MÁY TÍNH), nhiều máy in, một token.
type Client struct {
	baseURL string
	token   string
	version string

	// Hai http.Client tách nhau: long-poll cần hạn dài, còn báo kết quả và
	// heartbeat phải hỏng nhanh để không kẹt vòng lặp chính.
	pollHTTP  *http.Client
	shortHTTP *http.Client

	// Số nhịp còn phải NHỊN gửi kiểm kê thiết bị, sau khi ERP từ chối nó.
	//
	// ERP bản trước 047f bật `forbidNonWhitelisted`, nên trường `devices` lạ làm
	// CẢ gói nhịp sống rơi 400. Hậu quả không phải agent chết mà tệ hơn: nhịp hỏi
	// việc vẫn làm `last_seen_at` tươi, nên màn hình báo "máy quầy đang chạy · máy
	// in tốt" trong khi trạng thái cổng đứng hình ở lần báo cuối — màn hình nói dối.
	//
	// Nên khi bị từ chối thì bỏ kiểm kê ra, gửi lại ngay, và thử lại sau ít nhịp.
	// Thử lại chứ không tắt hẳn: ngày ERP được nâng cấp thì agent phải tự bật lên
	// lại, không đợi ai ra quán khởi động nó.
	devicesMuted int
}

// Số nhịp nhịn gửi kiểm kê sau một lần bị từ chối. 20 nhịp × 30 giây = 10 phút:
// đủ thưa để không quấy ERP cũ, đủ dày để quán được nâng cấp ERP lúc 9 giờ sáng thì
// chậm nhất 9h10 ô chọn thiết bị đã có dữ liệu.
const devicesRetryEvery = 20

func New(baseURL, token, version string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		version:   version,
		pollHTTP:  &http.Client{Timeout: pollTimeout},
		shortHTTP: &http.Client{Timeout: 10 * time.Second},
	}
}

// ErrNoJob — hết thời gian chờ mà không có việc. Đây là trạng thái BÌNH THƯỜNG
// (quán vắng khách), không phải lỗi: vòng lặp gọi lại ngay, không backoff.
var ErrNoJob = fmt.Errorf("chưa có việc in")

// NextJob chờ việc. Trả ErrNoJob khi hết giờ chờ.
func (c *Client) NextJob(ctx context.Context) (*Job, error) {
	url := fmt.Sprintf("%s/bridge/jobs/next?wait=%d", c.baseURL, int(PollWait.Seconds()))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)

	resp, err := c.pollHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không gọi được ERP: %w", err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, ErrNoJob
	case http.StatusOK:
		var j Job
		if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
			return nil, fmt.Errorf("đọc việc in từ ERP: %w", err)
		}
		return &j, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		// Token bị thu hồi hoặc máy in đã xoá bên ERP. Đây KHÔNG phải lỗi mạng
		// tạm thời — retry mãi cũng vô ích, phải báo người vận hành ghép lại.
		return nil, fmt.Errorf("ERP từ chối token (%d) — máy in có thể đã bị xoá hoặc thu hồi; chạy `pair` lại", resp.StatusCode)
	default:
		return nil, fmt.Errorf("ERP trả mã %d khi lấy việc in", resp.StatusCode)
	}
}

// ReportResult báo kết quả in.
//
// Đây là bước KHÔNG ĐƯỢC BỎ: ERP dựa vào nó để đánh dấu job xong hay hỏng, và
// để hiện "phiếu bếp chưa in được" ở quầy (PRD FR-047.16). In được mà không báo
// thì ERP tưởng vẫn hỏng và sẽ in lại — ra hai tờ.
func (c *Client) ReportResult(ctx context.Context, jobID int64, printErr error) error {
	body := map[string]any{"ok": printErr == nil}
	if printErr != nil {
		body["error"] = printErr.Error()
	}
	b, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/bridge/jobs/%d/result", c.baseURL, jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.shortHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("báo kết quả job %d: %w", jobID, err)
	}
	defer drain(resp)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("báo kết quả job %d: ERP trả mã %d", jobID, resp.StatusCode)
	}
	return nil
}

// Heartbeat cho ERP biết máy quầy còn sống và TỪNG máy in có mở cổng được không
// — nguồn dữ liệu cho cột trạng thái ở màn `/printers` (PRD FR-047.14).
//
// Gửi cả danh sách thay vì một cờ chung: các máy in trên cùng một máy tính hỏng
// độc lập, gộp lại thì rút dây máy in bếp sẽ kéo máy in bill báo đỏ theo và nhân
// viên đi kiểm nhầm chỗ.
// Kèm theo là KIỂM KÊ máy in đang cắm (047f) — trọn danh sách mỗi nhịp, không gửi
// delta, để ERP không bao giờ phải suy đoán bên nào đúng khi hai bên lệch nhau.
//
// Trả về `bindings_rev` của ERP. ERP bản cũ không có trường này → về 0, và người
// gọi hiểu là "không có gì để đồng bộ", chạy tiếp theo cấu hình cục bộ.
func (c *Client) Heartbeat(ctx context.Context, printers []PrinterStatus, devices []DeviceInfo) (int, error) {
	// Đang trong kỳ nhịn (ERP cũ vừa từ chối) thì bỏ kiểm kê ra cho tới lượt thử lại.
	if c.devicesMuted > 0 {
		c.devicesMuted--
		devices = nil
	}

	rev, err := c.heartbeatOnce(ctx, printers, devices)
	if err == nil || devices == nil || !errors.Is(err, errFieldRejected) {
		return rev, err
	}

	// ERP bản trước 047f không biết trường `devices`. Nhịn một lúc rồi gửi lại KHÔNG
	// kèm kiểm kê — thà mất ô chọn thiết bị trên web còn hơn mất trạng thái cổng máy in.
	c.devicesMuted = devicesRetryEvery
	return c.heartbeatOnce(ctx, printers, nil)
}

// errFieldRejected — ERP từ chối vì có trường nó không biết (400 của
// `forbidNonWhitelisted`), khác hẳn "ERP hỏng" hay "mất mạng".
var errFieldRejected = errors.New("ERP chưa biết trường này")

func (c *Client) heartbeatOnce(ctx context.Context, printers []PrinterStatus, devices []DeviceInfo) (int, error) {
	body := map[string]any{
		"version":  c.version,
		"printers": printers,
	}
	// Chỉ gửi khi dò được. `nil` khác `[]`: gửi mảng rỗng lúc chưa dò xong là khẳng
	// định "quán không cắm máy in nào" — ERP sẽ xoá sạch ô chọn của người dùng.
	if devices != nil {
		body["devices"] = devices
	}
	b, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bridge/heartbeat", bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.shortHTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusBadRequest && devices != nil {
		return 0, fmt.Errorf("%w: heartbeat 400 khi kèm kiểm kê thiết bị", errFieldRejected)
	}
	if resp.StatusCode >= 300 {
		return 0, fmt.Errorf("heartbeat: ERP trả mã %d", resp.StatusCode)
	}
	var out struct {
		BindingsRev int `json:"bindings_rev"`
	}
	// Bỏ qua lỗi giải mã có chủ đích: nhịp sống ĐÃ tới nơi, đó mới là việc chính.
	// ERP bản cũ trả `{"ok":true}` → rev 0 → không đồng bộ gì, đúng như mong đợi.
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.BindingsRev, nil
}

// DeviceInfo — một máy in vật lý agent nhìn thấy, gửi lên cho ERP đổ vào ô chọn.
//
// `device_key` do AGENT dựng, ERP chỉ lưu và so chuỗi. Giữ nguyên hướng này thì bản
// Windows sau chỉ cần đổi phía agent.
type DeviceInfo struct {
	DeviceKey string `json:"device_key"`
	Path      string `json:"path,omitempty"`
	Model     string `json:"model,omitempty"`
	Mfg       string `json:"mfg,omitempty"`
	CMD       string `json:"cmd,omitempty"`
	Serial    string `json:"serial,omitempty"`
	IDs       string `json:"ids,omitempty"`
	USBPath   string `json:"usb_path,omitempty"`
}

// RemoteBinding — một dòng trong bản gán ERP đang giữ.
type RemoteBinding struct {
	PrinterID   int    `json:"printer_id"`
	DeviceKey   string `json:"device_key"`
	PrinterName string `json:"printer_name"`
}

// Bindings tải bản gán máy in ↔ thiết bị từ ERP (047f).
//
// Chỉ gọi khi `bindings_rev` lệch, nên đường này gần như không chạy — mỗi lần chạy
// là một lần người ở ERP vừa bấm Lưu.
func (c *Client) Bindings(ctx context.Context) (int, []RemoteBinding, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/bridge/bindings", nil)
	if err != nil {
		return 0, nil, err
	}
	c.auth(req)

	resp, err := c.shortHTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer drain(resp)

	// 404 = ERP bản cũ chưa có đường này. Không phải lỗi, chỉ là không có gì để lấy.
	if resp.StatusCode == http.StatusNotFound {
		return 0, nil, nil
	}
	if resp.StatusCode >= 300 {
		return 0, nil, fmt.Errorf("tải bản gán: ERP trả mã %d", resp.StatusCode)
	}
	var out struct {
		BindingsRev int             `json:"bindings_rev"`
		Bindings    []RemoteBinding `json:"bindings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, nil, fmt.Errorf("tải bản gán: ERP trả dữ liệu lạ: %w", err)
	}
	return out.BindingsRev, out.Bindings, nil
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "trcf-bridge/"+c.version)
}

// drain đọc nốt body rồi đóng, để kết nối được tái dùng thay vì bị vứt —
// agent gọi endpoint này mỗi 25 giây suốt ngày.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}
