package escpos

import (
	"bytes"
	"testing"
)

// Trang in thử PHẢI kết thúc bằng lệnh cắt. Đây là thứ dễ mất nhất khi ai đó
// sửa nội dung trang thử rồi vô tình xoá đuôi — và mất nó thì giấy không đứt,
// người lắp tưởng máy in hỏng dao cắt.
func TestTestPageEndsWithCut(t *testing.T) {
	page := TestPage("v0.1.0", "/dev/usb/lp0")

	if !bytes.HasSuffix(page, cmdCut) {
		t.Fatalf("trang in thử không kết thúc bằng lệnh cắt giấy; 8 byte cuối = % X", page[len(page)-8:])
	}
	if !bytes.HasPrefix(page, cmdInit) {
		t.Fatalf("trang in thử không bắt đầu bằng ESC @ (khởi tạo); 4 byte đầu = % X", page[:4])
	}
}

// Trang thử phải chừa giấy TRƯỚC khi cắt, nếu không dao xén trúng dòng chữ cuối.
func TestTestPageFeedsBeforeCut(t *testing.T) {
	page := TestPage("v0.1.0", "/dev/usb/lp0")

	feedAt := bytes.LastIndex(page, cmdFeed4)
	cutAt := bytes.LastIndex(page, cmdCut)
	if feedAt < 0 {
		t.Fatal("thiếu lệnh đẩy giấy trước khi cắt")
	}
	if feedAt > cutAt {
		t.Fatalf("đẩy giấy (%d) phải đứng TRƯỚC lệnh cắt (%d)", feedAt, cutAt)
	}
}

// Dòng tiếng Việt có dấu là công cụ CHẨN ĐOÁN chính của trang thử (xem doc của
// TestPage): thiếu nó thì người lắp không phát hiện được máy in không hỗ trợ
// bảng mã tiếng Việt, và lỗi chỉ lộ ra khi quán đã bán hàng.
func TestTestPageCarriesVietnameseProbe(t *testing.T) {
	page := TestPage("v0.1.0", "/dev/usb/lp0")

	if !bytes.Contains(page, []byte("Cà phê sữa đá")) {
		t.Fatal("trang in thử thiếu dòng tiếng Việt có dấu để kiểm bảng mã")
	}
	if !bytes.Contains(page, []byte("<-32")) || !bytes.Contains(page, []byte("<-48")) {
		t.Fatal("trang in thử thiếu thước đo bề rộng 32/48 ký tự")
	}
}

// Đá két: ESC p m t1 t2 — 5 byte, m đổi theo chân RJ11.
func TestKickDrawerPins(t *testing.T) {
	tests := []struct {
		name string
		pin  int
		want byte
	}{
		{"chân 2 (mặc định)", 0, 0x00},
		{"chân 5", 1, 0x01},
		// Số lạ phải rơi về chân 2 chứ không được sinh byte rác khiến máy in
		// hiểu nhầm sang lệnh khác.
		{"số không hợp lệ về chân 2", 9, 0x00},
		{"số âm về chân 2", -1, 0x00},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := KickDrawer(tc.pin)
			want := []byte{0x1B, 0x70, tc.want, 0x19, 0xFA}
			if !bytes.Equal(got, want) {
				t.Fatalf("KickDrawer(%d) = % X, muốn % X", tc.pin, got, want)
			}
		})
	}
}

// Cut() phải trả bản SAO: nếu trả thẳng slice nội bộ thì một lệnh append của
// caller sẽ ghi đè hằng số dùng chung, làm hỏng mọi lần cắt sau đó.
func TestCutReturnsCopy(t *testing.T) {
	first := Cut()
	first[0] = 0xFF

	if second := Cut(); second[0] != 0x1D {
		t.Fatalf("Cut() bị sửa từ bên ngoài: byte đầu = %#x, muốn 0x1D", second[0])
	}
}
