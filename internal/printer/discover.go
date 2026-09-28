package printer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Dò thiết bị và đọc danh tính CỐ Ý không mang thẻ build `linux`.
//
// Nó chỉ đọc thư mục và tệp — không syscall, không phụ thuộc hệ điều hành. Tách
// ra đây để test chạy được trên máy phát triển (macOS) bằng một cây thư mục giả,
// đúng lý do `writeall.go` từng được tách khỏi `usb_linux.go`: đây là đoạn dễ sai
// mà lại không dựng lại được bằng máy in thật.
//
// Trên hệ không phải Linux, `/dev/usb/lp*` và `/sys/class/usbmisc` không tồn tại
// nên `Discover` trả danh sách rỗng — đúng hành vi mong muốn.
// Nơi đọc thiết bị. Là BIẾN chứ không phải hằng để test trỏ vào một cây thư mục
// giả — cùng lý do `usbWriteTimeout` là biến: đoạn logic này không dựng lại được
// bằng máy in thật trên máy phát triển, mà lại là đoạn dễ sai.
var (
	devGlobs        = []string{"/dev/usb/lp*", "/dev/usblp*"}
	sysClassUSBMisc = "/sys/class/usbmisc"
)

// Discover dò máy in USB đang cắm trên máy này.
//
// Nhân Linux nạp driver `usblp` cho thiết bị USB lớp printer và tạo device node
// /dev/usb/lp0 (một số bản dựng đặt ở /dev/usblp0). Không cần thư viện ngoài,
// không cần libusb, không đụng driver — chỉ đọc thư mục.
//
// CỐ Ý KHÔNG dùng libusb: nó buộc phải gỡ driver nhân ra, mà gỡ xong thì
// /dev/usb/lpN biến mất và đụng độ trực diện với CUPS — đúng cái bẫy đã tái diễn
// ở mọi máy quầy.
func Discover() ([]Found, error) {
	if usbBackend != nil {
		return usbBackend.Discover()
	}
	return discoverSysfs()
}

// discoverSysfs — cách dò của Linux (xem `Discover`). Không mang thẻ build để test
// dựng cây thư mục giả chạy được trên máy dev.
func discoverSysfs() ([]Found, error) {
	var paths []string
	for _, pattern := range devGlobs {
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

// IdentityForPath đọc danh tính của thiết bị đang nằm ở một đường dẫn.
//
// Dùng lúc `pair` (người lắp gõ --device) và lúc TỰ CHỮA cấu hình đời cũ: cấu
// hình chỉ có "/dev/usb/lp2" thì đây là cách biến nó thành một danh tính bền vững
// mà không cần ai gõ lại gì.
func IdentityForPath(path string) (Identity, error) {
	if _, err := os.Stat(path); err != nil {
		return Identity{}, fmt.Errorf("không thấy máy in tại %s — kiểm tra dây USB và nguồn máy in", path)
	}
	f := Found{Path: path}
	enrichFromSysfs(&f)
	if f.Identity.IsZero() {
		return Identity{}, fmt.Errorf("máy in tại %s không khai danh tính nào đọc được", path)
	}
	return f.Identity, nil
}

// enrichFromSysfs đọc danh tính máy in từ hệ thống tệp ảo của nhân.
//
// Mọi lỗi ở đây đều nuốt: thiếu một trường chỉ làm danh tính yếu đi (rơi xuống
// khoá lùi), không được phép làm hỏng việc dò — máy in không khai serial vẫn
// phải dùng được.
func enrichFromSysfs(f *Found) {
	base := filepath.Base(f.Path) // "lp0"

	// Lớp usbmisc là nơi usblp đăng ký node. Symlink "device" trỏ tới USB
	// interface; cha của nó là thiết bị USB mang serial/manufacturer/id.
	iface, err := filepath.EvalSymlinks(filepath.Join(sysClassUSBMisc, base, "device"))
	if err != nil {
		return
	}

	// ieee1284_id là chuỗi tự mô tả máy in cắm ra, dạng
	// "MFG:Xprinter;MDL:XP-80C;CMD:ESC/POS;". Đây là nguồn ĐÁNG TIN NHẤT về hãng,
	// model và ngôn ngữ lệnh — đáng tin hơn hẳn tên sản phẩm USB (xem Identity).
	if raw, err := os.ReadFile(filepath.Join(iface, "ieee1284_id")); err == nil {
		for _, part := range strings.Split(string(raw), ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(strings.ReplaceAll(v, "\x00", ""))
			switch strings.ToUpper(strings.TrimSpace(k)) {
			case "MFG", "MANUFACTURER":
				f.Identity.Mfg = v
			case "MDL", "MODEL":
				f.Identity.Model = v
			case "CMD", "COMMAND SET", "COMMANDSET":
				f.Identity.CMD = v
			case "SN", "SERN", "SERIALNUMBER":
				// Vài dòng máy khai serial ngay trong chuỗi 1284 mà không có tệp
				// `serial` riêng — nhận cả hai đường.
				f.Identity.Serial = v
			}
		}
	}

	dev := filepath.Dir(iface) // từ interface lên thiết bị USB
	if f.Identity.Mfg == "" {
		f.Identity.Mfg = readTrimmed(filepath.Join(dev, "manufacturer"))
	}
	if f.Identity.Model == "" {
		f.Identity.Model = readTrimmed(filepath.Join(dev, "product"))
	}

	// Serial của thiết bị USB — khoá MẠNH NHẤT, thứ duy nhất phân biệt được hai
	// chiếc CÙNG model. Ưu tiên tệp `serial` hơn chuỗi 1284 vì nó do chính lớp
	// USB khai.
	if sn := readTrimmed(filepath.Join(dev, "serial")); sn != "" {
		f.Identity.Serial = sn
	}

	if vid, pid := readTrimmed(filepath.Join(dev, "idVendor")), readTrimmed(filepath.Join(dev, "idProduct")); vid != "" && pid != "" {
		f.Identity.IDs = vid + ":" + pid
	}

	// Vị trí trên cây USB ("1-3" = bus 1, cổng 3). Khoá LÙI cho máy không khai
	// serial: nó trả lời "cắm ở cổng nào" thay vì "chiếc nào", nên đổi cổng là
	// mất khớp — chấp nhận được, vì không có nó thì hai máy giống hệt nhau sẽ
	// không tài nào phân biệt.
	if bus, dp := readTrimmed(filepath.Join(dev, "busnum")), readTrimmed(filepath.Join(dev, "devpath")); bus != "" && dp != "" {
		f.Identity.USBPath = bus + "-" + dp
	}
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", ""))
}
