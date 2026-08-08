//go:build linux

package printer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Đây là test QUAN TRỌNG NHẤT của đợt 047f: nó dựng lại đúng sự cố Gác Dero rồi
// chứng minh byte chảy vào ĐÚNG chiếc máy.
//
// Chỉ chạy trên Linux vì `openDevice` dùng syscall thô. Device node giả là một
// tệp thường — `usblp` không có ở đây, nhưng đường đi (phân giải → mở → ghi) là
// y hệt, và ta ĐỌC LẠI ĐƯỢC tệp để biết byte đã vào đâu.

// Kịch bản thật: cấu hình ghi "bill ở lp2"; sau khi khởi động máy POS, hai máy in
// về lp0/lp1. Trước 047f, agent ghi mù vào lp2 (không còn) hoặc vào chiếc đang
// chiếm chỗ — đó là lúc máy bill in ra chữ lệnh TSPL.
func TestWriteTimDungMayKhiDuongDanDaDoi(t *testing.T) {
	devDir := fakeSysfs(t, gacDeroDevices()...) // lp0 = bill, lp1 = tem

	p := &usbPrinter{
		path:     filepath.Join(devDir, "lp2"), // đường dẫn CŨ, nay không còn
		identity: &billGacDero,
	}

	if err := p.Write([]byte("HOA DON")); err != nil {
		t.Fatalf("phải in được dù đường dẫn đã đổi: %v", err)
	}

	if got := readFile(t, filepath.Join(devDir, "lp0")); got != "HOA DON" {
		t.Errorf("máy bill (lp0) nhận %q, muốn %q", got, "HOA DON")
	}
	// Điều quan trọng không kém: máy TEM phải KHÔNG nhận gì.
	if got := readFile(t, filepath.Join(devDir, "lp1")); got != "" {
		t.Errorf("máy tem (lp1) không được nhận gì, nhận %q", got)
	}
}

// Fail-closed: rút máy bill ra thì lệnh in phải HỎNG CÓ CHỮ, tuyệt đối không
// được vơ lấy máy tem còn lại. Đây là hành vi thay cho tờ giấy đầy chữ lệnh.
func TestWriteKhongCoMayThiHongChuKhongGhiBua(t *testing.T) {
	devs := gacDeroDevices()
	devDir := fakeSysfs(t, devs[1]) // chỉ còn máy TEM

	p := &usbPrinter{path: filepath.Join(devDir, "lp0"), identity: &billGacDero}

	err := p.Write([]byte("HOA DON"))
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("muốn ErrNoMatch, nhận %v", err)
	}
	if !strings.Contains(err.Error(), "POS-80") {
		t.Errorf("câu lỗi phải gọi tên máy: %v", err)
	}
	if got := readFile(t, filepath.Join(devDir, "lp1")); got != "" {
		t.Fatalf("ĐÃ GHI BỪA vào máy tem: %q — đây chính là lỗi đang sửa", got)
	}
}

// Cấu hình đời cũ (chưa có danh tính) vẫn chạy y như trước: ghi thẳng vào đường
// dẫn. Không có tính chất này thì nâng cấp binary là làm chết 5 quán đang chạy.
func TestWriteCauHinhDoiCuVanGhiTheoDuongDan(t *testing.T) {
	devDir := fakeSysfs(t, gacDeroDevices()...)

	p := &usbPrinter{path: filepath.Join(devDir, "lp1")} // identity nil
	if err := p.Write([]byte("CU")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readFile(t, filepath.Join(devDir, "lp1")); got != "CU" {
		t.Errorf("lp1 nhận %q", got)
	}
}

// Đường dẫn đổi GIỮA PHIÊN CHẠY (rút ra cắm lại) phải được phân giải lại và
// NÓI RA. Im lặng ở đây là bịt mất manh mối cho lần nó phân giải sai.
func TestWritePhanGiaiLaiVaNoiRaKhiDoiCho(t *testing.T) {
	var logs []string
	old := Logf
	Logf = func(format string, a ...any) { logs = append(logs, format) }
	defer func() { Logf = old }()

	devDir := fakeSysfs(t, gacDeroDevices()...)
	p := &usbPrinter{path: filepath.Join(devDir, "lp0"), identity: &billGacDero}
	if err := p.Write([]byte("A")); err != nil {
		t.Fatalf("lần 1: %v", err)
	}

	// Máy bill nhảy từ cổng 1-3 sang node lp5 (rút ra cắm lại cổng khác).
	moved := gacDeroDevices()[0]
	moved.node = "lp5"
	fakeSysfs(t, moved)

	if err := p.Write([]byte("B")); err != nil {
		t.Fatalf("lần 2: %v", err)
	}
	if !anyContains(logs, "đổi chỗ") {
		t.Errorf("phải ghi nhật ký khi máy in đổi chỗ, nhật ký: %v", logs)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("đọc %s: %v", path, err)
	}
	return string(b)
}

func anyContains(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}
