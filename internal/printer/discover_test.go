package printer

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSysfs dựng lại ĐÚNG hình dạng thật của sysfs, kể cả symlink:
//
//	/sys/class/usbmisc/lp0/device  →  /sys/devices/.../1-3/1-3:1.0   (interface)
//	                                  /sys/devices/.../1-3           (thiết bị USB)
//
// Dùng symlink thật chứ không xếp thư mục phẳng cho tiện, vì `enrichFromSysfs`
// đi qua `EvalSymlinks` rồi mới `filepath.Dir` để leo từ interface lên thiết bị.
// Xếp phẳng thì test vẫn xanh trong khi đường đi thật gãy — đúng loại test vô dụng.
type fakeDevice struct {
	node    string            // "lp0"
	usbPort string            // "1-3"
	iface   map[string]string // tệp ở tầng interface, vd ieee1284_id
	usb     map[string]string // tệp ở tầng thiết bị USB, vd serial/idVendor
}

func fakeSysfs(t *testing.T, devices ...fakeDevice) (devDir string) {
	t.Helper()
	root := t.TempDir()

	devDir = filepath.Join(root, "dev", "usb")
	classDir := filepath.Join(root, "sys", "class", "usbmisc")
	mustMkdir(t, devDir)
	mustMkdir(t, classDir)

	for _, d := range devices {
		usbDir := filepath.Join(root, "sys", "devices", "usb1", d.usbPort)
		ifaceDir := filepath.Join(usbDir, d.usbPort+":1.0")
		mustMkdir(t, ifaceDir)
		for name, val := range d.usb {
			mustWrite(t, filepath.Join(usbDir, name), val)
		}
		for name, val := range d.iface {
			mustWrite(t, filepath.Join(ifaceDir, name), val)
		}

		// Device node — nội dung không quan trọng, chỉ cần tồn tại để Glob thấy.
		mustWrite(t, filepath.Join(devDir, d.node), "")

		linkParent := filepath.Join(classDir, d.node)
		mustMkdir(t, linkParent)
		if err := os.Symlink(ifaceDir, filepath.Join(linkParent, "device")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
	}

	// Trỏ package vào cây giả trong đúng phạm vi test này.
	oldGlobs, oldClass := devGlobs, sysClassUSBMisc
	devGlobs = []string{filepath.Join(devDir, "lp*")}
	sysClassUSBMisc = classDir
	t.Cleanup(func() { devGlobs, sysClassUSBMisc = oldGlobs, oldClass })
	return devDir
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, val string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(val), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// Hai máy in THẬT của Gác Dero, khai đúng những gì đã đo ngày 2026-08-07.
func gacDeroDevices() []fakeDevice {
	return []fakeDevice{
		{
			node: "lp0", usbPort: "1-3",
			iface: map[string]string{"ieee1284_id": "MFG:Printer;MDL:POS-80;CMD:EPSON;"},
			usb: map[string]string{
				"serial": "1C79EC420000", "idVendor": "0483", "idProduct": "5743",
				"busnum": "1", "devpath": "3", "product": "USB Printer Port",
			},
		},
		{
			node: "lp1", usbPort: "1-4",
			iface: map[string]string{"ieee1284_id": "MFG:4BARCODE;MDL:3B-300B;CMD:TSPL2;"},
			usb: map[string]string{
				"serial": "00270A9D5193", "idVendor": "1fc9", "idProduct": "2016",
				"busnum": "1", "devpath": "4", "product": "Printer-80",
			},
		},
	}
}

func TestDiscoverDocDuDanhTinh(t *testing.T) {
	fakeSysfs(t, gacDeroDevices()...)

	found, err := Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("muốn 2 máy in, nhận %d", len(found))
	}

	bill := found[0].Identity
	if bill.Serial != "1C79EC420000" {
		t.Errorf("serial = %q", bill.Serial)
	}
	if bill.CMD != "EPSON" || CommandFamily(bill.CMD) != FamilyESCPOS {
		t.Errorf("máy bill phải là họ ESC/POS, nhận CMD=%q", bill.CMD)
	}
	if bill.IDs != "0483:5743" {
		t.Errorf("ids = %q", bill.IDs)
	}
	if bill.USBPath != "1-3" {
		t.Errorf("usb_path = %q", bill.USBPath)
	}

	tem := found[1].Identity
	if CommandFamily(tem.CMD) != FamilyTSPL {
		t.Errorf("máy tem phải là họ TSPL, nhận CMD=%q", tem.CMD)
	}
}

// Chuỗi IEEE 1284 phải THẮNG tên sản phẩm USB.
//
// Đây là bài học đắt nhất của quán: máy in TEM 4BARCODE tự khai tên sản phẩm USB
// là "Printer-80" — nghe hệt máy bill 80mm. Lấy tên USB đi đoán là chọn ngược
// 100%.
func TestIEEE1284ThangTenSanPhamUSB(t *testing.T) {
	fakeSysfs(t, gacDeroDevices()...)

	found, _ := Discover()
	tem := found[1].Identity
	if tem.Model != "3B-300B" {
		t.Errorf("phải lấy MDL của IEEE 1284, nhận %q (tên USB láo là 'Printer-80')", tem.Model)
	}
	if tem.Mfg != "4BARCODE" {
		t.Errorf("mfg = %q", tem.Mfg)
	}
}

// Máy không khai serial vẫn phải dò ra và vẫn có khoá lùi dùng được.
func TestDiscoverMayKhongKhaiSerial(t *testing.T) {
	fakeSysfs(t, fakeDevice{
		node: "lp0", usbPort: "2-1",
		iface: map[string]string{"ieee1284_id": "MFG:Xprinter;MDL:XP-80C;CMD:ESC/POS;"},
		usb:   map[string]string{"idVendor": "1a86", "idProduct": "7523", "busnum": "2", "devpath": "1"},
	})

	found, _ := Discover()
	if len(found) != 1 {
		t.Fatalf("muốn 1 máy in, nhận %d", len(found))
	}
	id := found[0].Identity
	if id.Serial != "" {
		t.Errorf("không có serial mà lại đọc ra %q", id.Serial)
	}
	if id.MatchKey() != "hw:1A86:7523@2-1" {
		t.Errorf("khoá lùi sai: %s", id.MatchKey())
	}
}

// Firmware nhét NUL vào chuỗi — bài học 048. Không cắt thì so khớp trượt và tên
// máy hiện ra thành một dấu cách.
func TestDiscoverCatKyTuNUL(t *testing.T) {
	fakeSysfs(t, fakeDevice{
		node: "lp0", usbPort: "1-1",
		iface: map[string]string{"ieee1284_id": "MFG:ACME\x00;MDL:T80\x00;CMD:ESC/POS;"},
		usb:   map[string]string{"serial": "AB123\x00", "idVendor": "0483", "idProduct": "5743", "busnum": "1", "devpath": "1"},
	})

	found, _ := Discover()
	id := found[0].Identity
	if id.Serial != "AB123" {
		t.Errorf("serial còn NUL: %q", id.Serial)
	}
	if id.Model != "T80" {
		t.Errorf("model còn NUL: %q", id.Model)
	}
}

// Vài dòng máy khai serial NGAY TRONG chuỗi 1284 mà không có tệp `serial`.
func TestDiscoverNhanSerialTuChuoi1284(t *testing.T) {
	fakeSysfs(t, fakeDevice{
		node: "lp0", usbPort: "1-2",
		iface: map[string]string{"ieee1284_id": "MFG:ACME;MDL:T80;CMD:ESC/POS;SN:XYZ999;"},
		usb:   map[string]string{"idVendor": "0483", "idProduct": "5743", "busnum": "1", "devpath": "2"},
	})

	found, _ := Discover()
	if got := found[0].Identity.Serial; got != "XYZ999" {
		t.Errorf("serial từ chuỗi 1284 = %q", got)
	}
}

// Thiết bị không có sysfs (bản dựng lạ, hoặc quyền đọc bị chặn) KHÔNG được làm
// hỏng việc dò — mất danh tính chỉ làm danh sách xấu đi.
func TestDiscoverKhongCoSysfsVanKhongVo(t *testing.T) {
	devDir := fakeSysfs(t) // không thiết bị nào
	mustWrite(t, filepath.Join(devDir, "lp7"), "")

	found, err := Discover()
	if err != nil {
		t.Fatalf("Discover không được lỗi: %v", err)
	}
	if len(found) != 1 || !found[0].Identity.IsZero() {
		t.Fatalf("muốn 1 máy in không danh tính, nhận %+v", found)
	}
	if found[0].Label() == "" {
		t.Error("vẫn phải có nhãn để người lắp chọn được")
	}
}

// IdentityForPath là đường TỰ CHỮA cấu hình đời cũ: từ "/dev/usb/lp0" suy ra
// chiếc máy đang nằm ở đó.
func TestIdentityForPathDungChoTuChua(t *testing.T) {
	devDir := fakeSysfs(t, gacDeroDevices()...)

	id, err := IdentityForPath(filepath.Join(devDir, "lp0"))
	if err != nil {
		t.Fatalf("IdentityForPath: %v", err)
	}
	if id.Serial != "1C79EC420000" {
		t.Errorf("suy nhầm máy: %+v", id)
	}

	// Đường dẫn không còn (đúng ca Gác Dero sau khởi động) → phải LỖI, để lớp
	// trên biết mà đừng đoán.
	if _, err := IdentityForPath(filepath.Join(devDir, "lp9")); err == nil {
		t.Error("đường dẫn không tồn tại thì phải báo lỗi, không được đoán")
	}
}
