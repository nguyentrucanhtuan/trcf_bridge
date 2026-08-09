// Package printer: chỗ DUY NHẤT phân nhánh theo cổng kết nối và hệ điều hành.
//
// Phần còn lại của agent không biết máy in nối bằng gì — đúng nguyên tắc
// "agent phải ngu" (xem README): agent chỉ nhận buffer ESC/POS đã dựng sẵn ở
// backend rồi ghi ra cổng.
package printer

import (
	"errors"
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

// writeTimeout: mốc MỀM cho một lần ghi — quá đây thì bắt đầu lo, chưa kết luận.
// Máy in nhiệt hết giấy sẽ ngừng nhận byte và treo lệnh ghi vô hạn; không có trần
// thì agent đứng im mãi mà không ai biết, đúng kiểu hỏng im lặng PRD §7 cấm.
const writeTimeout = 15 * time.Second

// writeGrace: chờ THÊM chừng này trước khi kết luận là hỏng.
//
// Vì sao phải có (sự cố Gác Đờ Rô 2026-08-09, 17/687 bill in lặp): lệnh ghi quá
// mốc mềm KHÔNG bị huỷ — goroutine ghi vẫn chạy và máy in vẫn đang nhả giấy. Báo
// hỏng ngay ở giây thứ 15 là nói dối: backend đưa lệnh về hàng đợi rồi gửi lại
// TRỌN payload, và tờ thứ hai chui ra trong khi tờ thứ nhất còn chưa in xong.
// Bằng chứng thấy rõ trong nhật ký: lượt thử lại ngay sau đó ăn EBUSY, tức đúng
// lúc ấy lệnh ghi cũ vẫn đang giữ thiết bị.
//
// 45 giây vì đo tại quán: lệnh in "kẹt" thật sự vẫn xong sau 60–90 giây kể từ lúc
// bắt đầu. Tổng trần cứng 60 giây vẫn nằm gọn dưới mốc thu hồi của backend.
const writeGrace = 45 * time.Second

// WriteError — lỗi in kèm câu trả lời cho câu hỏi DUY NHẤT backend cần biết để
// quyết có được tự in lại hay không: **byte đã kịp chạm giấy chưa?**
//
// Không phân biệt được hai loại hỏng này thì mọi chính sách thử lại đều sai một
// nửa: hoặc bỏ mất bill thật, hoặc in ra tờ thứ hai cho đơn đã có bill.
type WriteError struct {
	Err error
	// Delivered = đã đẩy được byte ra máy in (dù chỉ một phần), HOẶC không loại trừ
	// được khả năng đó. Backend tuyệt đối không tự in lại những lệnh này.
	Delivered bool
}

func (e *WriteError) Error() string { return e.Err.Error() }
func (e *WriteError) Unwrap() error { return e.Err }

// Delivered tra cờ trên. Lỗi KHÔNG phải WriteError đều xảy ra TRƯỚC khi có byte
// nào đi (phân giải đường dẫn, mở thiết bị, quay số TCP) → an toàn để in lại.
// Mặc định `false` là chủ đích: nhánh này chỉ dẫn tới "thử lại", mà thử lại một
// lệnh chưa hề chạm giấy thì không tốn gì.
func Delivered(err error) bool {
	var we *WriteError
	if errors.As(err, &we) {
		return we.Delivered
	}
	return false
}

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
