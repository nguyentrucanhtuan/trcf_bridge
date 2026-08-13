package printer

import (
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakePrinter đóng vai máy in LAN: nghe TCP, nuốt hết byte, trả lại cho test.
func fakePrinter(t *testing.T) (addr string, received <-chan []byte) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ch := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		b, _ := io.ReadAll(conn)
		ch <- b
	}()
	return ln.Addr().String(), ch
}

func TestNetPrinterSendsBytes(t *testing.T) {
	addr, received := fakePrinter(t)

	p, err := Open(Config{Kind: KindNet, Target: addr})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	want := []byte{0x1B, 0x40, 'H', 'I', 0x1D, 0x56, 0x42, 0x00}
	if err := p.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case got := <-received:
		if string(got) != string(want) {
			t.Fatalf("máy in nhận % X, muốn % X", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("máy in giả không nhận được gì sau 3s")
	}
}

// Cấu hình chỉ có IP là chuyện thường khi lắp đặt — phải tự hiểu là cổng 9100
// thay vì nổ lỗi khó hiểu.
func TestNetPrinterDefaultsToPort9100(t *testing.T) {
	p, err := Open(Config{Kind: KindNet, Target: "192.0.2.1"}) // dải TEST-NET, chắc chắn không có thật
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	err = p.Write([]byte("x"))
	if err == nil {
		t.Fatal("muốn lỗi kết nối tới địa chỉ không tồn tại")
	}
	if !strings.Contains(err.Error(), ":9100") {
		t.Errorf("không tự thêm cổng 9100 mặc định: %v", err)
	}
}

func TestOpenRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantHas string
	}{
		{"kiểu lạ", Config{Kind: "bluetooth", Target: "x"}, "không hợp lệ"},
		{"usb không target lẫn danh tính", Config{Kind: KindUSB}, "list"},
		{"usb danh tính rỗng", Config{Kind: KindUSB, Identity: &Identity{}}, "list"},
		{"net thiếu target", Config{Kind: KindNet}, "9100"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(tc.cfg)
			if err == nil {
				t.Fatal("muốn lỗi")
			}
			if !strings.Contains(err.Error(), tc.wantHas) {
				t.Errorf("thông báo %q không chứa %q", err.Error(), tc.wantHas)
			}
		})
	}
}

// Máy in chỉ có DANH TÍNH, chưa từng có đường dẫn — phải mở được.
//
// Đây là ca thật đã hỏng ở Gác Dero 2026-08-13: máy in "PHIẾU ĐÓNG CA" gán từ giao
// diện web nên cấu hình chỉ mang số sê-ri, `target` rỗng. Chốt chặn đời cũ đòi
// `target` đã loại nó khỏi vòng gom thiết bị ⇒ mọi lệnh in của nó hỏng câm.
//
// Kiểm bằng THÔNG BÁO chứ không bằng "có lỗi hay không", vì đường USB thật chỉ có
// trên Linux: trên máy dev macOS/Windows `openUSB` trả lỗi "chưa hỗ trợ" — vẫn là
// đã ĐI QUA được chốt chặn, đúng thứ test này canh.
func TestOpenUSBChiCoDanhTinhVanMoDuoc(t *testing.T) {
	p, err := Open(Config{Kind: KindUSB, Identity: &Identity{Serial: "1C79EC420000", Model: "POS-80"}})

	if err != nil && strings.Contains(err.Error(), "chưa chọn máy in USB") {
		t.Fatalf("danh tính đã đủ để mở máy in, nhưng bị chốt chặn đời cũ loại: %v", err)
	}
	if runtime.GOOS == "linux" && (err != nil || p == nil) {
		t.Fatalf("trên Linux phải dựng được máy in USB từ danh tính: p=%v err=%v", p, err)
	}
}
