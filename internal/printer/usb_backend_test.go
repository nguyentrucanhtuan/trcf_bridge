package printer

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// Hợp đồng mà Android (và mọi nền tảng sau này) phải giữ khi cắm đường USB riêng qua
// `SetUSBBackend`. Test chạy trên mọi hệ điều hành vì không đụng thiết bị thật: nó
// chứng minh các luật dùng chung — phân giải danh tính trước khi ghi, 0 khớp thì KHÔNG
// ghi bừa, cờ `Delivered` — vẫn áp nguyên khi nền tảng đổi.

// fakeBackend — máy in "cắm" trong bộ nhớ, khoá theo `Found.Path`.
type fakeBackend struct {
	found  []Found
	sinks  map[string]*fakeConn
	opened []string
}

func (b *fakeBackend) Discover() ([]Found, error) { return b.found, nil }

func (b *fakeBackend) Open(path string) (io.WriteCloser, error) {
	b.opened = append(b.opened, path)
	c, ok := b.sinks[path]
	if !ok {
		return nil, errors.New("không mở được " + path)
	}
	return c, nil
}

// fakeConn: `cut` bật thì nhận tối đa `limit` byte rồi báo lỗi — mô phỏng rút dây
// giữa chừng.
type fakeConn struct {
	buf   bytes.Buffer
	cut   bool
	limit int
}

func (c *fakeConn) Write(p []byte) (int, error) {
	if c.cut && c.buf.Len()+len(p) > c.limit {
		n := c.limit - c.buf.Len()
		c.buf.Write(p[:n])
		return n, errors.New("thiết bị rời bus")
	}
	return c.buf.Write(p)
}

func (c *fakeConn) Close() error { return nil }

func withBackend(t *testing.T, b USBBackend) {
	t.Helper()
	old := usbBackend
	SetUSBBackend(b)
	t.Cleanup(func() { usbBackend = old })
}

var (
	androidBill = Identity{Serial: "A1", IDs: "0483:5743", CMD: "ESC/POS", Model: "POS-80"}
	androidTem  = Identity{Serial: "T9", IDs: "1fc9:2016", CMD: "TSPL2", Model: "3B-300B"}
)

func TestBackendGhiDungMayTheoDanhTinh(t *testing.T) {
	bill, tem := &fakeConn{}, &fakeConn{}
	withBackend(t, &fakeBackend{
		// Đường dẫn Android đổi sau mỗi lần cắm lại — cấu hình chỉ giữ danh tính.
		found: []Found{
			{Path: "/dev/bus/usb/001/007", Identity: androidTem},
			{Path: "/dev/bus/usb/001/009", Identity: androidBill},
		},
		sinks: map[string]*fakeConn{"/dev/bus/usb/001/007": tem, "/dev/bus/usb/001/009": bill},
	})

	p, err := Open(Config{Kind: KindUSB, Identity: &androidBill})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.Write([]byte("BILL")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if bill.buf.String() != "BILL" || tem.buf.Len() != 0 {
		t.Fatalf("byte vào sai máy: bill=%q tem=%q", bill.buf.String(), tem.buf.String())
	}
}

func TestBackendKhongKhopThiKhongGhiBua(t *testing.T) {
	be := &fakeBackend{
		found: []Found{{Path: "/dev/bus/usb/001/007", Identity: androidTem}},
		sinks: map[string]*fakeConn{"/dev/bus/usb/001/007": {}},
	}
	withBackend(t, be)

	p, _ := Open(Config{Kind: KindUSB, Identity: &androidBill})
	err := p.Write([]byte("BILL"))
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("muốn ErrNoMatch, nhận %v", err)
	}
	if len(be.opened) != 0 {
		t.Fatalf("0 khớp mà vẫn mở thiết bị %v — đúng kiểu in bill ra máy tem", be.opened)
	}
	if Delivered(err) {
		t.Fatal("chưa chạm máy in thì phải cho ERP in lại (Delivered=false)")
	}
}

func TestBackendDeliveredTheoSoByteDaDi(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		want  bool
	}{
		{"hỏng ngay byte đầu → in lại được", 0, false},
		{"hỏng giữa chừng → cấm in lại", 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeConn{cut: true, limit: tc.limit}
			withBackend(t, &fakeBackend{
				found: []Found{{Path: "p", Identity: androidBill}},
				sinks: map[string]*fakeConn{"p": conn},
			})
			p, _ := Open(Config{Kind: KindUSB, Identity: &androidBill})
			err := p.Write([]byte("0123456789"))
			if err == nil {
				t.Fatal("muốn lỗi")
			}
			if got := Delivered(err); got != tc.want {
				t.Fatalf("Delivered=%v, muốn %v (%v)", got, tc.want, err)
			}
		})
	}
}

func TestChuaCoBackendThiBaoChuaHoTro(t *testing.T) {
	withBackend(t, nil)
	if _, err := Open(Config{Kind: KindUSB, Target: "/dev/usb/lp0"}); err == nil {
		t.Fatal("nền tảng chưa có đường USB mà Open vẫn nhận")
	}
}
