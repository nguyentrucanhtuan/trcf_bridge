package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/escpos"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/pair"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

// PairParams — mọi thứ một lần ghép cần. Vỏ nền tảng (CLI Linux, màn thiết lập
// Android) chỉ việc thu thập rồi giao cho `Pair`.
type PairParams struct {
	ConfigPath string
	Server     string
	Code       string
	Target     printer.Config
	Hostname   string
	OS         string // "linux" | "android" — ERP hiện ở màn Máy in
	Version    string
	// DrawerPin — nil = GIỮ chân đá két đang có. Chỉ đặt khi người lắp chủ động chọn:
	// ghi đè vô điều kiện thì quán dùng két chân 5 mất chức năng mở két ngay lúc ghép
	// THÊM máy in bếp, vì lần ghép thêm không ai chọn lại chân két.
	DrawerPin *int
}

// Pair ghép máy này với ERP cho MỘT máy in, theo đúng thứ tự an toàn:
//
//  1. In thử TRƯỚC — máy in hỏng thì dừng, chưa tạo bản ghi "đã ghép" sai bên ERP.
//  2. Đọc cấu hình cũ (giữ token để ERP GẮN THÊM máy in vào chính máy quầy này).
//  3. Kiểm ghi được cấu hình TRƯỚC khi gọi ERP — gọi xong mới phát hiện không ghi
//     nổi thì bên ERP máy in đã chuyển sang máy quầy này mà máy này không có token,
//     máy in đang in tốt ngừng in trong im lặng.
//  4. Gọi ERP, rồi lưu.
//
// `say` nhận các dòng tiến trình cho người lắp đọc (nil = im lặng).
func Pair(ctx context.Context, p PairParams, say func(string)) (*pair.Result, *config.Config, error) {
	if say == nil {
		say = func(string) {}
	}
	server := strings.TrimRight(strings.TrimSpace(p.Server), "/")
	code := strings.TrimSpace(p.Code)
	if server == "" || code == "" {
		return nil, nil, fmt.Errorf("thiếu địa chỉ ERP hoặc mã ghép")
	}

	dev, err := printer.Open(p.Target)
	if err != nil {
		return nil, nil, err
	}
	say("Đang in thử trước khi ghép ...")
	if err := dev.Write(escpos.TestPage(p.Version, p.Target.Target)); err != nil {
		return nil, nil, fmt.Errorf("máy in chưa in được, dừng ghép nối: %w", err)
	}
	say("✓ Máy in nhận lệnh.")

	// Phân biệt "chưa có tệp" với "tệp có mà đọc không xong": dựng Config rỗng cho
	// MỌI lỗi sẽ vứt sạch token và danh sách máy in đã ghép chỉ vì một trường thiếu.
	cfg, err := config.LoadForPairing(p.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	if err := config.CheckWritable(p.ConfigPath); err != nil {
		return nil, nil, err
	}

	res, err := pair.Do(ctx, server, pair.Request{
		Code:        code,
		Hostname:    p.Hostname,
		OS:          p.OS,
		PrinterDesc: PrinterDesc(p.Target),
		Version:     p.Version,
	}, cfg.Token)
	if err != nil {
		return nil, nil, err
	}

	cfg.ServerURL = server
	cfg.Token = res.Token
	if p.DrawerPin != nil {
		cfg.DrawerPin = *p.DrawerPin
	}
	cfg.Upsert(config.Binding{PrinterID: res.PrinterID, Printer: p.Target})
	if err := config.Save(p.ConfigPath, cfg); err != nil {
		return nil, nil, err
	}
	return res, cfg, nil
}

// PrinterDesc — chuỗi mô tả gửi lên ERP lúc ghép.
//
// `Key()` chứ KHÔNG phải `Label()`: chuỗi này về ERP thành `device_key`, và ERP so
// nó bằng `===` với khoá trong danh sách thiết bị agent báo mỗi nhịp. Gửi nhãn người
// đọc thì hai chuỗi không bao giờ khớp — ô chọn trên web sẽ báo "thiết bị không còn"
// cho một máy in đang in tốt. Nhãn đẹp do ERP tự tra từ kiểm kê.
func PrinterDesc(cfg printer.Config) string {
	if id := cfg.Identity; id != nil && !id.IsZero() {
		return id.Key()
	}
	return cfg.Target
}
