//go:build linux

package printer

import (
	"fmt"
	"sync"
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
	path     string    // đường dẫn ghi trong cấu hình — GỢI Ý khi chưa có danh tính
	identity *Identity // nil = cấu hình đời cũ, chạy theo `path` như trước

	mu       sync.Mutex
	lastPath string // đường dẫn đã phân giải lần gần nhất, để biết khi nào nó ĐỔI
}

// Write phân giải danh tính → mở device → ghi trọn buffer → đóng.
//
// Mở-ghi-đóng từng lần thay vì giữ handle mở suốt: máy in nhiệt hay bị rút
// nguồn/cắm lại giữa ca. Giữ handle thì sau khi cắm lại nó trỏ vào device đã
// chết mà không báo lỗi rõ; mở lại mỗi lần thì lần in kế tiếp tự nhận device
// mới. Bill in mỗi vài phút nên chi phí mở file là không đáng kể.
func (p *usbPrinter) Write(b []byte) error {
	path, err := p.resolvePath()
	if err != nil {
		return err
	}
	dev, err := openDevice(path)
	if err != nil {
		return err
	}
	return writeUSB(dev, b, path)
}

// resolvePath tìm chỗ chiếc máy này ĐANG nằm, ngay trước khi ghi.
//
// Phân giải TRƯỚC MỖI LẦN IN chứ không chỉ lúc khởi động — đây là điểm cốt lõi
// của cả đợt sửa. Chỉ phân giải lúc khởi động thì rút–cắm giữa ca sẽ lệch lại,
// mà lần đó KHÔNG có cú khởi động nào để ai đó nghi ngờ: quán chỉ thấy tự dưng
// bill chui ra máy in tem.
//
// Chi phí: một lượt quét /dev/usb/lp* + đọc vài tệp sysfs, cỡ mili-giây — không
// đáng gì so với 15 giây trần ghi USB và bill cỡ 32 KB.
func (p *usbPrinter) resolvePath() (string, error) {
	if p.identity == nil || p.identity.IsZero() {
		return p.path, nil
	}

	devices, err := Discover()
	if err != nil {
		return "", err
	}
	res, err := Resolve(*p.identity, devices)
	if err != nil {
		return "", err
	}

	// Nói ra khi có chuyện. "Tự chữa lành trong im lặng" cũng là một dạng hỏng
	// im lặng: nếu đường dẫn nhảy mỗi lần khởi động mà không ai thấy, thì lúc nó
	// nhảy SAI cũng chẳng ai thấy.
	p.mu.Lock()
	changed := p.lastPath != "" && p.lastPath != res.Device.Path
	first := p.lastPath == ""
	p.lastPath = res.Device.Path
	p.mu.Unlock()

	switch {
	case changed:
		Logf("máy in %s đổi chỗ: %s → %s", p.identity.Label(), p.path, res.Device.Path)
	case first && res.Device.Path != p.path:
		Logf("máy in %s nay ở %s (cấu hình ghi %s)", p.identity.Label(), res.Device.Path, p.path)
	}
	if res.Warning != "" {
		Logf("máy in %s: %s", p.identity.Label(), res.Warning)
	}
	return res.Device.Path, nil
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
		case syscall.ENODEV, syscall.ENXIO:
			// ENODEV ≠ ENOENT. Node vẫn nằm trong /dev và sysfs vẫn đọc được (nên bước
			// dò danh tính phía trên đã khớp đúng serial), nhưng mở ra thì nhân báo
			// "không có thiết bị" — hình dạng kinh điển của MÁY IN BỊ TẮT NGUỒN trong
			// khi mạch USB của nó vẫn được cấp điện từ dây, nên vẫn còn trên bus.
			//
			// Thiếu nhánh này thì câu lỗi rơi xuống dòng dưới và người ở quán nhận
			// đúng ba chữ tiếng Anh "no such device" — vô nghĩa với họ, lại còn nghe
			// như hỏng hóc nặng trong khi việc cần làm chỉ là bật máy in lên.
			// (Gác Đờ Rô 08/08: 48 hoá đơn hỏng liên tiếp chỉ hiện câu ấy.)
			return nil, fmt.Errorf("máy in %s đang TẮT hoặc đã rớt kết nối USB — bật nguồn máy in lên, hoặc rút cắm lại dây USB; nếu quán cố ý không dùng máy in này thì vào ERP mục Máy in tắt ô \"Đang bật\" để khỏi dồn lệnh in hỏng", path)
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
