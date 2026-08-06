//go:build linux

package printer

import (
	"fmt"
	"syscall"
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
	dev, err := openDevice(p.path)
	if err != nil {
		return err
	}
	return writeUSB(dev, b, p.path)
}

func (p *usbPrinter) String() string { return "usb:" + p.path }

// rawDevice — thiết bị mở bằng SYSCALL THÔ, cố ý KHÔNG dùng `os.File`.
//
// ─────────────────────────────────────────────────────────────────────────
// ĐÂY LÀ CHỖ ĐÃ LÀM MẤT BILL — đo bằng strace tại quán 2026-08-06:
//
//	openat("/dev/usb/lp4", O_WRONLY|O_CLOEXEC) = 3
//	fcntl(3, F_SETFL, O_WRONLY|O_NONBLOCK|O_LARGEFILE) = 0   ← Go tự bật
//	write(3, ..., 466) = 466
//	close(3)
//
// `os.OpenFile` đăng ký fd vào bộ theo dõi sự kiện của Go runtime, và bước đó
// BẬT O_NONBLOCK. Device node là thiết bị ký tự nên Go coi là "theo dõi được"
// → luôn bật, KHÔNG cần ai gọi SetWriteDeadline (bản vá đầu tiên gỡ deadline
// mà vẫn hỏng y nguyên chính vì hiểu nhầm chỗ này).
//
// Với `usblp`, non-blocking đổi hẳn ngữ nghĩa write(): nó trả về NGAY khi byte
// vừa được xếp vào hàng đợi USB, CHƯA chạy qua dây. `close()` ngay sau đó gọi
// `usblp_release()` → huỷ các URB dang dở → phần đuôi bill BIẾN MẤT.
//
// Triệu chứng ở quán: bill và tờ in thử đều chỉ ra 2 dòng đầu, không có lệnh
// cắt, mà agent vẫn báo "đã in xong" — hỏng im lặng đúng nghĩa. Ghi cùng bộ
// byte đó bằng Python (fd chặn) thì ra đủ.
//
// Syscall thô giữ fd ở chế độ CHẶN: write() chỉ trả về sau khi dữ liệu đã thực
// sự đi qua cổng USB, nên lúc đóng không còn gì để mất.
// ─────────────────────────────────────────────────────────────────────────
type rawDevice struct {
	fd   int
	path string
}

func openDevice(path string) (*rawDevice, error) {
	// KHÔNG O_CREAT: device không tồn tại thì phải BÁO LỖI, tuyệt đối không được
	// tạo ra một file thường cùng tên rồi "in" vào đó và tưởng là thành công.
	// KHÔNG O_NONBLOCK: xem khối chú thích trên.
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		switch err {
		case syscall.ENOENT:
			return nil, fmt.Errorf("không thấy máy in tại %s — kiểm tra dây USB và nguồn máy in", path)
		case syscall.EACCES, syscall.EPERM:
			return nil, fmt.Errorf("không có quyền ghi vào %s — thêm tài khoản chạy agent vào nhóm 'lp' hoặc cài udev rule (xem README)", path)
		case syscall.EBUSY:
			return nil, fmt.Errorf("máy in %s đang bị chương trình khác giữ — thường là CUPS; xem README mục gỡ CUPS", path)
		}
		return nil, fmt.Errorf("mở %s: %w", path, err)
	}
	return &rawDevice{fd: fd, path: path}, nil
}

func (d *rawDevice) Write(p []byte) (int, error) {
	for {
		n, err := syscall.Write(d.fd, p)
		if err == syscall.EINTR {
			// Bị tín hiệu cắt ngang giữa chừng — thử lại, không coi là lỗi in.
			continue
		}
		if err != nil {
			return 0, err
		}
		return n, nil
	}
}

func (d *rawDevice) Close() error { return syscall.Close(d.fd) }
