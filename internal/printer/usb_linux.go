//go:build linux

package printer

import (
	"fmt"
	"os"
	"time"
)

// usbPrinter ghi ESC/POS THẲNG vào device node của nhân Linux (`usblp`),
// thường là /dev/usb/lp0.
//
// Vì sao không đi qua CUPS: CUPS mặc định dựng ảnh raster rồi nhờ driver dịch
// sang lệnh máy in. Bill của mình ĐÃ là ESC/POS hoàn chỉnh (backend dựng) nên
// đi qua CUPS chỉ thêm một tầng có thể bóp méo byte. Ghi thẳng device là đường
// ngắn nhất và dễ suy luận nhất khi có sự cố.
type usbPrinter struct {
	path string
}

// Write mở device, ghi trọn buffer, đóng.
//
// Mở-ghi-đóng từng lần thay vì giữ handle mở suốt: máy in nhiệt hay bị rút
// nguồn/cắm lại giữa ca. Giữ handle thì sau khi cắm lại nó trỏ vào device đã
// chết mà không báo lỗi rõ; mở lại mỗi lần thì lần in kế tiếp tự nhận device
// mới. Bill in mỗi vài phút nên chi phí mở file là không đáng kể.
func (p *usbPrinter) Write(b []byte) error {
	// O_WRONLY thuần — không O_CREATE: nếu device không tồn tại thì phải BÁO LỖI,
	// tuyệt đối không được tạo ra một file thường cùng tên rồi "in" vào đó và
	// tưởng là thành công (hỏng im lặng — thứ PRD §7 cấm).
	f, err := os.OpenFile(p.path, os.O_WRONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("không thấy máy in tại %s — kiểm tra dây USB và nguồn máy in", p.path)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("không có quyền ghi vào %s — thêm tài khoản chạy agent vào nhóm 'lp' hoặc cài udev rule (xem README)", p.path)
		}
		return fmt.Errorf("mở %s: %w", p.path, err)
	}
	defer f.Close()

	if err := f.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		// Một số device node không nhận deadline — không sao, ghi tiếp không
		// deadline còn hơn là bỏ cả lần in.
		_ = err
	}

	return writeAll(f, b, p.path)
}

func (p *usbPrinter) String() string { return "usb:" + p.path }
