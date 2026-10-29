package printer

import (
	"io"
	"runtime"
	"sync"
)

// USBBackend — phần DUY NHẤT của đường USB phụ thuộc hệ điều hành: dò máy in đang
// cắm (kèm danh tính) và mở một luồng ghi CHẶN tới một máy.
//
// Mọi thứ còn lại — phân giải danh tính trước mỗi lần in, hai mốc chờ 15s + 45s,
// cờ `Delivered`, ghi-cho-tới-hết — nằm ở `usbPrinter`/`writeUSB`/`writeAll` và
// DÙNG CHUNG cho mọi nền tảng. Đó là các luật sinh ra từ sự cố thật ở quán; cho
// mỗi nền tảng tự viết lại thì chúng sẽ lệch nhau.
//
//   - Linux: `/dev/usb/lp*` + sysfs (usb_linux.go), tự đăng ký lúc khởi động.
//   - Android: app cài đặt bằng USB Host API rồi gọi `SetUSBBackend`.
//
// Hợp đồng của `Open`: `Write` chỉ trả về khi byte đã thực sự đi ra cổng (chế độ
// CHẶN) và trả đúng số byte đã đi — `writeAll` dựa vào con số đó để quyết
// `Delivered`. `Found.Path` do `Discover` trả là thứ `Open` nhận lại.
type USBBackend interface {
	Discover() ([]Found, error)
	Open(path string) (io.WriteCloser, error)
}

// usbBackend — nil trên nền tảng chưa có đường USB (máy dev macOS/Windows).
var usbBackend USBBackend

// SetUSBBackend thay đường USB của nền tảng. Gọi MỘT lần, trước khi mở máy in nào.
func SetUSBBackend(b USBBackend) { usbBackend = b }

// unsupportedError — nền tảng chưa có đường USB. Đường KindNet vẫn chạy trên mọi
// hệ điều hành, nên `test --net` dùng được ngay trên máy dev để thử máy in LAN.
type unsupportedError struct{ op string }

func (e *unsupportedError) Error() string {
	return e.op + " chưa hỗ trợ trên " + runtime.GOOS + " (hiện chỉ Linux; Windows làm ở đợt sau)"
}

// openUSB KHÔNG kiểm tra device có tồn tại hay không — cố ý.
//
// Agent phải khởi động được cả khi máy in đang tắt hoặc chưa cắm, rồi báo lỗi
// THEO TỪNG LẦN IN để ERP hiện "máy in mất kết nối" (PRD FR-047.9/.14). Nếu
// chặn ngay lúc Open thì máy in tắt buổi tối = agent chết, sáng bật máy in lên
// vẫn không in được vì agent đã thoát từ đêm qua.
func openUSB(target string, id *Identity) (Printer, error) {
	if usbBackend == nil {
		return nil, &unsupportedError{op: "kết nối máy in USB"}
	}
	return &usbPrinter{path: target, identity: id}, nil
}

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
	dev, err := usbBackend.Open(path)
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
