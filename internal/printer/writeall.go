package printer

import (
	"fmt"
	"io"
)

// writeAll ghi cho tới hết buffer.
//
// Vì sao không dùng thẳng io.Writer.Write một lần: device node của máy in
// (usblp) có thể nhận TỪNG PHẦN khi bộ đệm máy in đầy — hay gặp với bill dài
// hoặc bill có logo. Coi lần ghi ngắn là "xong" thì bill CỤT ĐUÔI, và cái mất
// đầu tiên là lệnh cắt giấy nằm ở cuối → giấy không đứt, nhân viên phải xé tay
// và tưởng máy in hỏng dao.
//
// Tách khỏi usb_linux.go để test được trên mọi hệ điều hành: đây là đoạn logic
// dễ sai nhất trong agent mà lại không thể dựng lại bằng máy in thật.
// Cờ `Delivered` gắn ngay tại đây vì đây là chỗ DUY NHẤT biết đã đẩy đi bao nhiêu
// byte. Ghi hỏng ở byte thứ 0 thì giấy chưa hề chạy (in lại được); hỏng ở byte thứ
// 30.000 thì nửa tờ bill đã nằm trên khay (in lại là ra hai tờ).
func writeAll(w io.Writer, b []byte, name string) error {
	for written := 0; written < len(b); {
		n, err := w.Write(b[written:])
		if err != nil {
			return &WriteError{
				Err:       fmt.Errorf("ghi %s (đã gửi %d/%d byte): %w", name, written, len(b), err),
				Delivered: written > 0,
			}
		}
		if n <= 0 {
			// Không tiến thêm byte nào mà cũng không báo lỗi: nếu cứ lặp tiếp thì
			// agent quay vô hạn và im lặng. Dừng và báo — hết giấy/kẹt giấy là
			// nguyên nhân thường gặp nhất.
			return &WriteError{
				Err:       fmt.Errorf("ghi %s dừng ở %d/%d byte — máy in không nhận thêm (hết giấy? kẹt giấy?)", name, written, len(b)),
				Delivered: written > 0,
			}
		}
		written += n
	}
	return nil
}
