package printer

import (
	"fmt"
	"io"
	"time"
)

// writeUSB ghi trọn buffer rồi đóng thiết bị — GIỮ NGUYÊN chế độ chặn (blocking).
//
// ─────────────────────────────────────────────────────────────────────────
// Vì sao KHÔNG đặt hạn ghi (SetWriteDeadline) trên device — bug 2026-08-06,
// đo tại quán trên máy in thật:
//
// `SetWriteDeadline` buộc Go đăng ký fd vào bộ theo dõi sự kiện của runtime, và
// bước đó BẬT CỜ O_NONBLOCK trên thiết bị. Với `usblp`, non-blocking đổi hẳn
// ngữ nghĩa của write(): nó trả về NGAY khi byte vừa được xếp vào hàng đợi USB,
// CHƯA hề chạy qua dây. Ngay sau đó `Close()` chạy → `usblp_release()` huỷ các
// URB còn dang dở → phần đuôi bill BIẾN MẤT.
//
// Ở chế độ chặn, write() chỉ trả về sau khi dữ liệu đã thực sự đi qua cổng USB,
// nên lúc đóng thiết bị không còn gì để mất. Việc GIỮ fd ở chế độ chặn là trách
// nhiệm của `openDevice` (usb_linux.go) — đọc khối chú thích ở đó, vì đúng chỗ
// ấy là nơi Go âm thầm bật O_NONBLOCK và làm mất bill.
//
// Trần thời gian: trước đây do `SetWriteDeadline` giữ, nhưng chính nó là thứ
// kéo fd sang non-blocking. Nay trần nằm ở đây, ngoài tầng syscall.
// ─────────────────────────────────────────────────────────────────────────
// Biến (không phải hằng) để test rút ngắn được — test treo thật 15 giây thì
// không ai chạy nữa, mà đây lại đúng là nhánh cần có test.
var usbWriteTimeout = writeTimeout

func writeUSB(f io.WriteCloser, b []byte, name string) error {
	done := make(chan error, 1)
	// Goroutine SỞ HỮU thiết bị: nó ghi xong thì chính nó đóng. Không chỗ nào
	// khác được đóng — xem nhánh quá hạn bên dưới.
	go func() {
		err := writeAll(f, b, name)
		cerr := f.Close()
		if err == nil && cerr != nil {
			// Lỗi lúc đóng cũng phải báo: usblp đẩy nốt phần cuối khi close, nên
			// close hỏng nghĩa là đuôi bill có thể chưa kịp ra giấy.
			err = fmt.Errorf("đóng %s sau khi ghi: %w", name, cerr)
		}
		done <- err
	}()

	select {
	case err := <-done:
		return err

	case <-time.After(usbWriteTimeout):
		// CỐ Ý KHÔNG đóng thiết bị ở đây. Đóng một fd thô trong khi luồng khác
		// đang kẹt giữa write() là mở đường cho lỗi tệ hơn hẳn: số hiệu fd được
		// cấp lại cho tệp mới mở, và cú write đang treo sẽ đổ byte máy in vào
		// đúng tệp đó. Thà rò một fd (máy in kẹt là tình huống báo động, không
		// phải chuyện thường ngày) — mà rò cũng tự chặn được: `usblp` chỉ cho
		// MỘT tiến trình mở, nên lệnh in kế tiếp nhận EBUSY kèm câu chỉ rõ.
		return fmt.Errorf("máy in %s không nhận hết dữ liệu trong %s — kiểm tra giấy, nắp máy và dây USB", name, usbWriteTimeout)
	}
}
