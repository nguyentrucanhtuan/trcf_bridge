//go:build linux

package printer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// openUSB KHÔNG kiểm tra device có tồn tại hay không — cố ý.
//
// Agent phải khởi động được cả khi máy in đang tắt hoặc chưa cắm, rồi báo lỗi
// THEO TỪNG LẦN IN để ERP hiện "máy in mất kết nối" (PRD FR-047.9/.14). Nếu
// chặn ngay lúc Open thì máy in tắt buổi tối = agent chết, sáng bật máy in lên
// vẫn không in được vì agent đã thoát từ đêm qua.
func openUSB(target string) (Printer, error) {
	return &usbPrinter{path: target}, nil
}

// Discover dò máy in USB đang cắm trên máy này.
//
// Nhân Linux nạp driver `usblp` cho thiết bị USB lớp printer và tạo device node
// /dev/usb/lp0 (một số bản dựng đặt ở /dev/usblp0). Không cần thư viện ngoài,
// không cần libusb, không đụng driver — chỉ đọc thư mục.
func Discover() ([]Found, error) {
	var paths []string
	for _, pattern := range []string{"/dev/usb/lp*", "/dev/usblp*"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		paths = append(paths, m...)
	}
	sort.Strings(paths)

	found := make([]Found, 0, len(paths))
	for _, p := range paths {
		f := Found{Path: p}
		enrichFromSysfs(&f)
		found = append(found, f)
	}
	return found, nil
}

// enrichFromSysfs đọc tên hãng/model để người lắp đặt CHỌN ĐƯỢC bằng mắt.
//
// "/dev/usb/lp0" với "/dev/usb/lp1" thì không ai biết cái nào là máy in bill,
// cái nào là máy in bếp. Đọc thêm được "Xprinter XP-80C" là chọn đúng ngay.
// Mọi lỗi ở đây đều nuốt: thiếu tên chỉ làm danh sách xấu đi, không được phép
// làm hỏng việc dò.
func enrichFromSysfs(f *Found) {
	base := filepath.Base(f.Path) // "lp0"

	// Lớp usbmisc là nơi usblp đăng ký node. Symlink "device" trỏ tới USB
	// interface; cha của nó là thiết bị USB mang manufacturer/product/id.
	iface, err := filepath.EvalSymlinks("/sys/class/usbmisc/" + base + "/device")
	if err != nil {
		return
	}

	// ieee1284_id là chuỗi tự mô tả máy in cắm ra, dạng
	// "MFG:Xprinter;MDL:XP-80C;CMD:ESC/POS;". Có thì dùng, vì nó chính xác nhất.
	if raw, err := os.ReadFile(filepath.Join(iface, "ieee1284_id")); err == nil {
		for _, part := range strings.Split(string(raw), ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), ":")
			if !ok {
				continue
			}
			switch strings.ToUpper(k) {
			case "MFG", "MANUFACTURER":
				f.Vendor = strings.TrimSpace(v)
			case "MDL", "MODEL":
				f.Model = strings.TrimSpace(v)
			}
		}
	}

	dev := filepath.Dir(iface) // từ interface lên thiết bị USB
	if f.Vendor == "" {
		f.Vendor = readTrimmed(filepath.Join(dev, "manufacturer"))
	}
	if f.Model == "" {
		f.Model = readTrimmed(filepath.Join(dev, "product"))
	}

	// idVendor:idProduct để in ra udev rule cho người lắp đặt dán vào máy —
	// đây là thứ khoá chặt tên device, chống lp0 đổi thành lp1 sau khi cắm lại.
	if vid, pid := readTrimmed(filepath.Join(dev, "idVendor")), readTrimmed(filepath.Join(dev, "idProduct")); vid != "" && pid != "" {
		f.IDs = vid + ":" + pid
	}
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
