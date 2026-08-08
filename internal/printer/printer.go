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

// Logf — nơi package này nói ra những chuyện KHÔNG phải lỗi nhưng phải nhìn
// thấy được: máy in đổi đường dẫn, khớp bằng khoá lùi vì thiếu serial.
//
// Mặc định câm để `test`/`list` không lẫn tạp; `run` gắn nhật ký thật vào đây.
// Có hook này vì im lặng khi mọi thứ "tự chữa lành" cũng là hỏng im lặng — lúc
// nó tự chữa SAI thì cũng chẳng ai thấy.
var Logf = func(format string, a ...any) {}

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
//   - Identity (chỉ USB): DANH TÍNH chiếc máy — nguồn sự thật thật sự. Khi có nó,
//     `Target` tụt xuống làm GỢI Ý: agent tự dò lại đường dẫn hiện thời trước mỗi
//     lần in. Cố ý GIỮ `Target` thay vì xoá, vì hai lý do: nó là manh mối để tự
//     chữa cấu hình đời cũ, và nó khiến bản binary CŨ vẫn đọc được cấu hình MỚI —
//     tức là hạ cấp binary là đường lùi đủ, không phải sửa tệp tay ở quán lúc 7 giờ sáng.
type Config struct {
	Kind     string    `json:"kind"`
	Target   string    `json:"target"`
	Identity *Identity `json:"identity,omitempty"`
}

// Open dựng Printer theo cấu hình. Đây là ranh giới duy nhất mà phần còn lại
// của agent chạm vào chuyện "nối bằng gì".
func Open(cfg Config) (Printer, error) {
	switch cfg.Kind {
	case KindUSB:
		if cfg.Target == "" {
			return nil, fmt.Errorf("chưa chọn máy in USB — chạy `trcf-bridge list` để xem máy in đang cắm")
		}
		return openUSB(cfg.Target, cfg.Identity)
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
//
// `Path` là chỗ nó ĐANG nằm hôm nay (nhân Linux đặt, đổi sau mỗi lần khởi động);
// `Identity` là chiếc máy đó LÀ AI (bền vững). Hai thứ này trước đây bị gộp làm
// một chuỗi — chính là lỗi mà đợt sửa này gỡ ra.
type Found struct {
	Path     string   // đường dẫn hiện thời, vd "/dev/usb/lp0"
	Identity Identity // danh tính bền vững đọc từ sysfs
}

// Label — tên hiển thị cho người lắp đặt chọn.
func (f Found) Label() string {
	if f.Identity.IsZero() {
		return f.Path
	}
	return fmt.Sprintf("%s (%s)", f.Identity.Label(), f.Path)
}
