package core

import (
	"testing"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

// Dùng máy in LAN cho các test này: `printer.Open` với KindNet không đụng hệ điều
// hành và không mở kết nối nào lúc Open, nên chạy được trên mọi máy phát triển.
// Tính chất đang kiểm — GOM THEO THIẾT BỊ — không phụ thuộc loại cổng.
func netBinding(id int, addr string) config.Binding {
	return config.Binding{
		PrinterID: id,
		Printer:   printer.Config{Kind: printer.KindNet, Target: addr},
	}
}

func usbBinding(id int, path string, ident printer.Identity) config.Binding {
	return config.Binding{
		PrinterID: id,
		Printer:   printer.Config{Kind: printer.KindUSB, Target: path, Identity: &ident},
	}
}

// Hai vai trò in trên MỘT máy phải dùng CHUNG một hàng đợi.
//
// Đây là ca dùng thật của quán: một POS-80 in cả bill lẫn phiếu bếp. Nếu mỗi máy
// in ERP một thợ thì hai thợ tranh nhau cùng một device node — `usblp` chỉ cho
// một tiến trình mở, nên một lệnh in sẽ ăn EBUSY và HỎNG, mà câu lỗi lại đổ cho
// CUPS.
func TestGomChungHangDoiKhiDungChungThietBi(t *testing.T) {
	cfg := &config.Config{Printers: []config.Binding{
		netBinding(1, "192.168.1.60:9100"), // Bill quầy
		netBinding(2, "192.168.1.60:9100"), // Phiếu bếp — CÙNG máy
	}}

	groups, routes, err := groupByDevice(cfg)
	if err != nil {
		t.Fatalf("groupByDevice: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("muốn 1 thiết bị, nhận %d", len(groups))
	}
	if routes[1] != routes[2] {
		t.Fatal("hai máy in ERP cùng thiết bị PHẢI trỏ vào cùng một hàng đợi")
	}
	if got := routes[1].jobs; got == nil {
		t.Fatal("thiếu hàng đợi")
	}
	if len(routes[1].printerIDs) != 2 {
		t.Errorf("nhóm phải nhớ cả 2 máy in ERP, nhận %v", routes[1].printerIDs)
	}
}

// Hai thiết bị khác nhau PHẢI độc lập — máy in bếp kẹt giấy không được chặn bill
// cho khách đang đứng đợi.
func TestHaiThietBiKhacNhauThiDocLap(t *testing.T) {
	cfg := &config.Config{Printers: []config.Binding{
		netBinding(1, "192.168.1.60:9100"),
		netBinding(2, "192.168.1.61:9100"),
	}}

	groups, routes, err := groupByDevice(cfg)
	if err != nil {
		t.Fatalf("groupByDevice: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("muốn 2 thiết bị, nhận %d", len(groups))
	}
	if routes[1] == routes[2] {
		t.Error("hai thiết bị khác nhau không được chung hàng đợi")
	}
}

// Gom theo DANH TÍNH, không theo đường dẫn.
//
// Chi tiết dễ sai nhất của cả đợt sửa: hai binding của cùng một chiếc máy có thể
// mang đường dẫn khác nhau (một cái ghi từ hôm cài, một cái vừa ghép lại). Gom
// theo đường dẫn thì ra hai thợ cho một cổng — quay lại đúng lỗi đang sửa.
func TestGomTheoDanhTinhKhongTheoDuongDan(t *testing.T) {
	id := printer.Identity{Serial: "1C79EC420000", CMD: "EPSON", IDs: "0483:5743", USBPath: "1-3"}
	cfg := &config.Config{Printers: []config.Binding{
		usbBinding(1, "/dev/usb/lp2", id), // ghi từ hôm cài
		usbBinding(2, "/dev/usb/lp0", id), // ghép lại sau khi khởi động
	}}

	groups, routes, err := groupByDevice(cfg)
	if err != nil {
		// Trên máy không phải Linux, Open USB trả lỗi → không có nhóm nào. Bỏ qua
		// thay vì báo hỏng: tính chất này đã được kiểm ở `config.DeviceKey`.
		t.Skip("đường USB chỉ mở được trên Linux:", err)
	}
	if len(groups) != 1 {
		t.Fatalf("cùng một chiếc máy phải ra 1 nhóm, nhận %d", len(groups))
	}
	if routes[1] != routes[2] {
		t.Error("cùng danh tính mà khác đường dẫn vẫn phải chung hàng đợi")
	}
}

// Không mở nổi máy in nào thì phải nói ra, đừng chạy vòng lặp rỗng và im lặng.
func TestKhongCoMayInNaoThiBaoLoi(t *testing.T) {
	if _, _, err := groupByDevice(&config.Config{}); err == nil {
		t.Fatal("muốn lỗi khi không có máy in nào")
	}
}

// Nhãn hiển thị ưu tiên danh tính — đường dẫn chỉ là chỗ nó nằm hôm nay.
func TestDescribeBindingUuTienDanhTinh(t *testing.T) {
	id := printer.Identity{Serial: "1C79EC420000", CMD: "EPSON", Model: "POS-80", Mfg: "Printer"}
	if got := describeBinding(usbBinding(1, "/dev/usb/lp0", id)); got == "/dev/usb/lp0" {
		t.Errorf("có danh tính thì đừng hiện đường dẫn: %q", got)
	}

	cu := config.Binding{PrinterID: 2, Printer: printer.Config{Kind: printer.KindUSB, Target: "/dev/usb/lp2"}}
	if got := describeBinding(cu); got != "/dev/usb/lp2" {
		t.Errorf("cấu hình đời cũ thì rơi về đường dẫn, nhận %q", got)
	}
}
