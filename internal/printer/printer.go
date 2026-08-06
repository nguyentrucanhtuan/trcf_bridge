// Package printer: chỗ DUY NHẤT phân nhánh theo cổng kết nối và hệ điều hành.
//
// Phần còn lại của agent không biết máy in nối bằng gì — đúng nguyên tắc
// "agent phải ngu" (xem README): agent chỉ nhận buffer ESC/POS đã dựng sẵn ở
// backend rồi ghi ra cổng.
package printer

import (
	"fmt"
	"time"
)

// writeTimeout: trần thời gian cho MỘT lần ghi. Máy in nhiệt hết giấy sẽ ngừng
// nhận byte và treo lệnh ghi vô hạn — không có trần thì agent đứng im mãi mà
// không ai biết, đúng kiểu hỏng im lặng PRD §7 cấm.
const writeTimeout = 15 * time.Second

// Printer là toàn bộ bề mặt mà agent cần ở một máy in.
//
// b là byte ESC/POS THÔ do backend dựng — agent không diễn giải, không sửa,
// không chèn thêm gì (kể cả lệnh cắt giấy: backend đã đặt vào đúng chỗ).
type Printer interface {
	Write(b []byte) error
}

// Kind — cách nối tới máy in.
const (
	KindUSB = "usb" // ghi vào device node (Linux) / spooler RAW (Windows, làm sau)
	KindNet = "net" // TCP tới IP:9100
)

// Config mô tả một máy in như agent thấy nó.
//
// Target nghĩa gì tuỳ Kind:
//   - KindUSB: đường dẫn device trên Linux (vd "/dev/usb/lp0", hoặc symlink cố
//     định "/dev/trcf-printer" do udev tạo — xem README).
//   - KindNet: "IP:port", thường cổng 9100. Đây là đường DỰ PHÒNG: khi backend
//     chạy cùng LAN thì nó tự mở TCP, không nhờ agent. Chỉ dùng khi backend
//     không với tới máy in (backend trên cloud, hoặc máy in khác VLAN).
type Config struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// Open dựng Printer theo cấu hình. Đây là ranh giới duy nhất mà phần còn lại
// của agent chạm vào chuyện "nối bằng gì".
func Open(cfg Config) (Printer, error) {
	switch cfg.Kind {
	case KindUSB:
		if cfg.Target == "" {
			return nil, fmt.Errorf("chưa chọn máy in USB — chạy `trcf-bridge list` để xem máy in đang cắm")
		}
		return openUSB(cfg.Target)
	case KindNet:
		if cfg.Target == "" {
			return nil, fmt.Errorf("chưa có địa chỉ máy in mạng, cần dạng IP:9100")
		}
		return &netPrinter{addr: cfg.Target}, nil
	default:
		return nil, fmt.Errorf("kiểu kết nối không hợp lệ %q — chỉ nhận %q hoặc %q", cfg.Kind, KindUSB, KindNet)
	}
}

// Found — một máy in agent dò thấy trên máy này.
type Found struct {
	Path   string // đường dẫn để đưa vào Config.Target
	Vendor string // tên hãng đọc từ hệ thống, có thể rỗng
	Model  string // tên model đọc từ hệ thống, có thể rỗng
	IDs    string // "idVendor:idProduct" — dùng để viết udev rule
}

// Label — tên hiển thị cho người lắp đặt chọn.
func (f Found) Label() string {
	switch {
	case f.Vendor != "" && f.Model != "":
		return fmt.Sprintf("%s %s (%s)", f.Vendor, f.Model, f.Path)
	case f.Model != "":
		return fmt.Sprintf("%s (%s)", f.Model, f.Path)
	default:
		return f.Path
	}
}
