package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

// Hai máy in thật ở Gác Dero, dưới dạng khoá mà ERP cất trong `device_key`.
var (
	keyBill = printer.Identity{
		Serial: "1C79EC420000", CMD: "EPSON", IDs: "0483:5743", USBPath: "1-3", Model: "POS-80",
	}.Key()
	keyTem = printer.Identity{
		Serial: "00270A9D5193", CMD: "TSPL2", IDs: "1fc9:2016", USBPath: "1-4", Model: "3B-300B",
	}.Key()
)

func usbBind(id int, target string, ident printer.Identity) Binding {
	return Binding{PrinterID: id, Printer: printer.Config{Kind: printer.KindUSB, Target: target, Identity: &ident}}
}

// Đổi thiết bị trên web: máy in bill được trỏ sang chiếc máy khác.
//
// Đây là toàn bộ lý do Pha 2 tồn tại — thao tác trước đây phải SSH vào quán sửa tệp.
func TestApplyBindingsDoiThietBiTheoERP(t *testing.T) {
	c := &Config{Printers: []Binding{usbBind(7, "/dev/usb/lp0", printer.Identity{Serial: "CU"})}}

	changed, warns := c.ApplyBindings([]DeviceAssignment{{PrinterID: 7, DeviceKey: keyBill}})
	if !changed {
		t.Fatal("đổi thiết bị mà báo không có gì đổi")
	}
	if len(warns) != 0 {
		t.Errorf("không nên cảnh báo gì: %v", warns)
	}
	got := c.Printers[0].Printer.Identity
	if got == nil || got.Serial != "1C79EC420000" {
		t.Fatalf("chưa nhận danh tính mới: %+v", got)
	}
	if got.CMD != "EPSON" {
		t.Errorf("mất họ lệnh sau khi áp: %q", got.CMD)
	}
	// `target` cũ phải GIỮ: nó là đường lùi nếu ai đó hạ binary về bản trước 047f.
	if c.Printers[0].Printer.Target != "/dev/usb/lp0" {
		t.Errorf("mất target cũ: %q", c.Printers[0].Printer.Target)
	}
}

// Áp đúng bản đang chạy → KHÔNG được báo có thay đổi.
//
// Quan trọng hơn vẻ ngoài: `changed` là thứ kích hoạt nạp lại phiên. Báo bừa thì agent
// nạp lại mỗi 30 giây, và mỗi lần nạp lại là một lần rút lui + dựng lại thợ in.
func TestApplyBindingsKhongDoiThiKhongBaoDoi(t *testing.T) {
	c := &Config{Printers: []Binding{usbBind(7, "/dev/usb/lp0", printer.Identity{
		Serial: "1C79EC420000", CMD: "EPSON", IDs: "0483:5743", USBPath: "1-3", Model: "POS-80",
	})}}

	if changed, _ := c.ApplyBindings([]DeviceAssignment{{PrinterID: 7, DeviceKey: keyBill}}); changed {
		t.Error("cùng một thiết bị mà báo có thay đổi → agent sẽ nạp lại vô tận")
	}
}

// Gỡ máy in trên web thì agent phải BUÔNG ra, không giữ lại kéo việc.
func TestApplyBindingsGoMotMayInThiBienMat(t *testing.T) {
	c := &Config{Printers: []Binding{
		usbBind(7, "/dev/usb/lp0", printer.Identity{Serial: "1C79EC420000"}),
		usbBind(9, "/dev/usb/lp1", printer.Identity{Serial: "00270A9D5193"}),
	}}

	changed, _ := c.ApplyBindings([]DeviceAssignment{{PrinterID: 7, DeviceKey: keyBill}})
	if !changed {
		t.Fatal("bớt một máy in mà báo không đổi")
	}
	if len(c.Printers) != 1 || c.Printers[0].PrinterID != 7 {
		t.Fatalf("còn lại phải đúng máy in 7: %+v", c.Printers)
	}
}

// 🎯 Ngoại lệ có chủ đích: danh sách RỖNG không được xoá sạch cấu hình đang chạy.
//
// Hai hỏng hóc bất đối xứng — xoá nhầm thì cả quán ngừng in mà agent vẫn xanh (không ai
// hiểu vì sao), giữ thừa thì vô hại. Nên khi ERP nói "không có gì cả" thì giữ và kêu to.
func TestApplyBindingsRongKhongXoaSachCauHinh(t *testing.T) {
	c := &Config{Printers: []Binding{usbBind(7, "/dev/usb/lp0", printer.Identity{Serial: "1C79EC420000"})}}

	changed, warns := c.ApplyBindings(nil)
	if changed {
		t.Fatal("KHÔNG được coi là có thay đổi — đây là đường làm cả quán mất máy in")
	}
	if len(c.Printers) != 1 {
		t.Fatalf("ĐÃ XOÁ SẠCH cấu hình đang chạy: %+v", c.Printers)
	}
	if len(warns) == 0 {
		t.Error("giữ nguyên trong im lặng cũng là hỏng im lặng — phải cảnh báo")
	}
}

// Thêm máy in mới hoàn toàn (vai trò thứ hai trên cùng chiếc máy) mà KHÔNG chạy lại
// lệnh ghép ở quán. Đây là ca dùng chung thiết bị mà Pha 1 vừa mở đường.
func TestApplyBindingsThemVaiTroThuHaiTrenCungMay(t *testing.T) {
	c := &Config{Printers: []Binding{usbBind(7, "/dev/usb/lp0", printer.Identity{Serial: "1C79EC420000"})}}

	changed, warns := c.ApplyBindings([]DeviceAssignment{
		{PrinterID: 7, DeviceKey: keyBill},
		{PrinterID: 8, DeviceKey: keyBill}, // phiếu bếp, CÙNG chiếc máy
	})
	if !changed || len(c.Printers) != 2 {
		t.Fatalf("phải thêm được máy in 8: changed=%v %+v", changed, c.Printers)
	}
	if len(warns) != 0 {
		t.Errorf("dùng chung thiết bị là hợp lệ, không cảnh báo ở đây: %v", warns)
	}
	// Cùng khoá thiết bị ⇒ phiên mới sẽ gom chung MỘT hàng đợi, không tranh cổng.
	if c.Printers[0].DeviceKey() != c.Printers[1].DeviceKey() {
		t.Error("hai vai trò cùng chiếc máy phải cho ra cùng khoá gom nhóm")
	}
}

// Người quản lý phát hiện hai vai trò bị gán ngược nhau và đổi chéo lại trên web.
//
// Chính là cách chữa sự cố Gác Dero bằng chuột thay vì bằng SSH — và là ca dễ sai nếu
// việc áp bản gán làm theo kiểu vá từng dòng: vá dòng đầu xong thì giữa chừng có hai
// máy in cùng trỏ vào một chiếc, rồi mới vá dòng sau.
func TestApplyBindingsDoiCheoHaiVaiTro(t *testing.T) {
	c := &Config{Printers: []Binding{
		usbBind(7, "/dev/usb/lp0", printer.Identity{Serial: "00270A9D5193"}), // bill ← đang trỏ máy TEM
		usbBind(9, "/dev/usb/lp1", printer.Identity{Serial: "1C79EC420000"}), // tem  ← đang trỏ máy BILL
	}}

	changed, _ := c.ApplyBindings([]DeviceAssignment{
		{PrinterID: 7, DeviceKey: keyBill},
		{PrinterID: 9, DeviceKey: keyTem},
	})
	if !changed {
		t.Fatal("đổi chéo mà báo không đổi")
	}
	if c.Printers[0].Printer.Identity.Serial != "1C79EC420000" {
		t.Errorf("máy in 7 chưa về máy bill: %+v", c.Printers[0].Printer.Identity)
	}
	if c.Printers[1].Printer.Identity.Serial != "00270A9D5193" {
		t.Errorf("máy in 9 chưa về máy tem: %+v", c.Printers[1].Printer.Identity)
	}
	if c.Printers[0].DeviceKey() == c.Printers[1].DeviceKey() {
		t.Error("sau khi đổi chéo, hai vai trò phải nằm trên hai chiếc máy khác nhau")
	}
}

// Máy in LAN: danh tính của nó vốn đã bền vững, đừng đụng vào.
func TestApplyBindingsMayInLAN(t *testing.T) {
	c := &Config{}
	changed, warns := c.ApplyBindings([]DeviceAssignment{{PrinterID: 3, DeviceKey: "192.168.1.60:9100"}})
	if !changed || len(c.Printers) != 1 {
		t.Fatalf("phải dựng được máy in LAN: %+v", c.Printers)
	}
	if len(warns) != 0 {
		t.Errorf("không nên cảnh báo: %v", warns)
	}
	if c.Printers[0].Printer.Kind != printer.KindNet || c.Printers[0].Printer.Target != "192.168.1.60:9100" {
		t.Errorf("dựng sai máy in LAN: %+v", c.Printers[0].Printer)
	}
}

// Khoá đời cũ (ERP còn giữ đường dẫn từ trước 047f) → GIỮ mục đang chạy, đừng phá.
//
// Đây là trạng thái của cả 5 quán ngay sau khi nâng cấp: agent mới, ERP vẫn còn
// `device_key` là "/dev/usb/lp2" của lần ghép cũ.
func TestApplyBindingsKhoaDoiCuThiGiuNguyen(t *testing.T) {
	cu := usbBind(7, "/dev/usb/lp0", printer.Identity{Serial: "1C79EC420000", CMD: "EPSON"})
	c := &Config{Printers: []Binding{cu}}

	changed, warns := c.ApplyBindings([]DeviceAssignment{{PrinterID: 7, DeviceKey: "/dev/usb/lp2"}})
	if changed {
		t.Error("khoá đời cũ không được làm agent nạp lại")
	}
	if c.Printers[0].Printer.Identity == nil || c.Printers[0].Printer.Identity.Serial != "1C79EC420000" {
		t.Fatalf("đã phá danh tính vừa tự chữa: %+v", c.Printers[0].Printer)
	}
	if len(warns) != 0 {
		t.Errorf("giữ nguyên được thì đừng làm ồn: %v", warns)
	}
}

// 🎯 Máy in gán LẦN ĐẦU trên web (chưa từng chạy `pair`) không có `target` nào — chỉ có
// danh tính. Tệp ấy phải NẠP LẠI ĐƯỢC.
//
// Không có tính chất này thì đúng thao tác mà 047f sinh ra để phục vụ lại giết agent ở
// lần khởi động kế: cấu hình vừa ghi bị chính `Load` từ chối, cả quán mất đường in mà
// tệp trông hoàn toàn hợp lệ với người đọc.
func TestApplyBindingsMucMoiKhongCoTargetVanNapLaiDuoc(t *testing.T) {
	c := &Config{ServerURL: "http://erp", Token: "tok"}
	if changed, _ := c.ApplyBindings([]DeviceAssignment{{PrinterID: 7, DeviceKey: keyBill}}); !changed {
		t.Fatal("phải dựng được mục mới")
	}
	if c.Printers[0].Printer.Target != "" {
		t.Fatalf("ca này phải KHÔNG có target: %q", c.Printers[0].Printer.Target)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load từ chối cấu hình do chính ApplyBindings ghi ra: %v", err)
	}
	if got.Printers[0].Printer.Identity.Serial != "1C79EC420000" {
		t.Errorf("mất danh tính qua vòng ghi–đọc: %+v", got.Printers[0].Printer)
	}
}

// Máy in ERP mới toanh mà khoá không hiểu được → không dựng bừa, phải NÓI RA.
func TestApplyBindingsKhoaLaMayInMoiThiNoiRa(t *testing.T) {
	c := &Config{}
	changed, warns := c.ApplyBindings([]DeviceAssignment{{PrinterID: 5, DeviceKey: "máy in ở quầy"}})
	if changed || len(c.Printers) != 0 {
		t.Fatalf("không được dựng mục từ khoá không hiểu: %+v", c.Printers)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "5") {
		t.Errorf("cảnh báo phải gọi tên máy in: %v", warns)
	}
}

// Danh tính ERP gửi xuống hỏng → không được ghi đè mục đang chạy bằng thứ hỏng đó.
func TestApplyBindingsDanhTinhHongThiGiuMucCu(t *testing.T) {
	cu := usbBind(7, "/dev/usb/lp0", printer.Identity{Serial: "1C79EC420000"})
	c := &Config{Printers: []Binding{cu}}

	changed, warns := c.ApplyBindings([]DeviceAssignment{{PrinterID: 7, DeviceKey: "usb://%zz?serial=x"}})
	if changed {
		t.Error("khoá hỏng không được coi là thay đổi hợp lệ")
	}
	if c.Printers[0].Printer.Identity.Serial != "1C79EC420000" {
		t.Fatalf("đã ghi đè bằng danh tính hỏng: %+v", c.Printers[0].Printer.Identity)
	}
	if len(warns) == 0 {
		t.Error("phải nói ra khi không đọc được thứ ERP gửi xuống")
	}
}
