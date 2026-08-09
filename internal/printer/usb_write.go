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
var (
	usbWriteTimeout = writeTimeout
	usbWriteGrace   = writeGrace
)

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
			//
			// `Delivered: true` — tới được đây nghĩa là writeAll đã ghi TRỌN buffer
			// (fd ở chế độ chặn nên byte đã thực sự qua cổng USB), chỉ mỗi cú đóng là
			// hỏng. Giấy đã ra gần đủ, thiếu cùng lắm là phần đuôi. Để mặc nó thành
			// lỗi thường thì backend xếp lại vào hàng đợi và in NGUYÊN một tờ thứ hai
			// — đúng thứ phải chặn: một máy in, một tờ.
			err = &WriteError{
				Err:       fmt.Errorf("đóng %s sau khi ghi: %w", name, cerr),
				Delivered: true,
			}
		}
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(usbWriteTimeout):
	}

	// ─────────────────────────────────────────────────────────────────────────
	// Quá mốc mềm. TUYỆT ĐỐI KHÔNG báo hỏng ở đây — sự cố Gác Đờ Rô 2026-08-09.
	//
	// Lệnh ghi bên trên KHÔNG bị huỷ (không huỷ được, và cố ý không đóng fd — xem
	// khối dưới). Nó vẫn đang chảy byte ra máy in. Bản trước trả lỗi ngay tại mốc
	// này, backend hiểu là "chưa in được" rồi gửi lại trọn payload → tờ thứ hai
	// chui ra trong khi tờ thứ nhất còn chưa in xong. 17/687 bill của quán dính,
	// và nhật ký cho thấy lượt thử lại ăn EBUSY — tức đúng lúc đó lệnh ghi cũ vẫn
	// đang giữ thiết bị, bằng chứng trực tiếp là giấy VẪN đang ra.
	//
	// Nên: chờ tiếp. Máy in chỉ khựng (bill ảnh 36–46 KB, giấy gần hết, USB chập)
	// thì nó xong sau vài chục giây và đây KHÔNG phải lỗi.
	// ─────────────────────────────────────────────────────────────────────────
	Logf("máy in %s ghi lâu hơn %s — chờ thêm tối đa %s trước khi kết luận (KHÔNG in lại lúc này)",
		name, usbWriteTimeout, usbWriteGrace)

	select {
	case err := <-done:
		if err == nil {
			Logf("máy in %s: ghi xong sau khi chờ thêm — không phải lỗi, đã in đủ", name)
		}
		return err

	case <-time.After(usbWriteGrace):
		// CỐ Ý KHÔNG đóng thiết bị ở đây. Đóng một fd thô trong khi luồng khác
		// đang kẹt giữa write() là mở đường cho lỗi tệ hơn hẳn: số hiệu fd được
		// cấp lại cho tệp mới mở, và cú write đang treo sẽ đổ byte máy in vào
		// đúng tệp đó. Thà rò một fd (máy in kẹt là tình huống báo động, không
		// phải chuyện thường ngày) — mà rò cũng tự chặn được: `usblp` chỉ cho
		// MỘT tiến trình mở, nên lệnh in kế tiếp nhận EBUSY kèm câu chỉ rõ.
		//
		// `Delivered: true` là điểm mấu chốt: hết cả hai mốc mà lệnh ghi vẫn chưa
		// về nghĩa là nó đã đẩy được kha khá byte đi rồi. Giấy nhiều khả năng đã
		// ra (đủ hoặc thiếu) → backend KHÔNG được tự in lại, phải để người ở quán
		// nhìn máy in rồi tự quyết.
		return &WriteError{
			Err: fmt.Errorf("máy in %s không nhận hết dữ liệu trong %s — giấy có thể đã ra một phần; kiểm tra giấy, nắp máy và dây USB rồi bấm In lại nếu cần",
				name, usbWriteTimeout+usbWriteGrace),
			Delivered: true,
		}
	}
}
