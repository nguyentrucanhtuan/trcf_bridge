//go:build linux

package main

import (
	"testing"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

// Máy in gán từ web (chỉ danh tính, `target` rỗng) phải có hàng đợi.
//
// Ca thật ở Gác Dero 2026-08-13. "PHIẾU ĐÓNG CA" và "MÁY IN BILL QUẦY" cùng trỏ một
// chiếc POS-80; bản gán của phiếu đóng ca đến từ giao diện web nên chỉ mang số sê-ri.
// Chốt chặn đời cũ trong `printer.Open` đòi `target` ⇒ `groupByDevice` BỎ QUA nó ⇒
// mọi lệnh in phiếu đóng ca hỏng câm, ERP vẫn báo "đã gửi".
//
// Nguy hiểm hơn: lỗi phụ thuộc THỨ TỰ. Hôm bản gán xếp máy in bill lên trước, chính
// nó mở hộ thiết bị nên phiếu đóng ca in được; hôm ERP đẩy bản gán mới đảo thứ tự là
// mất đường in. Ca dưới đây cố ý đặt bản gán thiếu `target` LÊN ĐẦU — thứ tự xấu.
//
// Chỉ chạy trên Linux vì đường USB thật chỉ có ở đó (`usb_other.go` trả "chưa hỗ trợ").
// Chính khoảng trống này để lọt lỗi: mọi test gom nhóm sẵn có đều dùng máy in LAN.
func TestGomNhomNhanMayInChiCoDanhTinh(t *testing.T) {
	pos80 := printer.Identity{Serial: "1C79EC420000", CMD: "EPSON", Model: "POS-80"}

	cfg := &config.Config{Printers: []config.Binding{
		// Phiếu đóng ca — gán từ web, chưa từng có đường dẫn.
		{PrinterID: 3, Printer: printer.Config{Kind: printer.KindUSB, Identity: &pos80}},
		// Bill quầy — CÙNG chiếc máy, có sẵn đường dẫn đời cũ.
		{PrinterID: 1, Printer: printer.Config{Kind: printer.KindUSB, Target: "/dev/usb/lp2", Identity: &pos80}},
	}}

	groups, routes, err := groupByDevice(cfg)
	if err != nil {
		t.Fatalf("groupByDevice: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("cùng một chiếc máy phải gom thành 1 thiết bị, nhận %d", len(groups))
	}
	if routes[3] == nil {
		t.Fatal("máy in chỉ có danh tính bị bỏ rơi — đúng lỗi phiếu đóng ca ở Gác Dero")
	}
	if routes[1] != routes[3] {
		t.Fatal("hai vai trò trên cùng chiếc máy phải dùng chung hàng đợi")
	}
}
