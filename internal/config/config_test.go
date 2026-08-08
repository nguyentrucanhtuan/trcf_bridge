package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

func usb(target string) printer.Config {
	return printer.Config{Kind: printer.KindUSB, Target: target}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")

	want := &Config{
		ServerURL: "http://192.168.1.50",
		Token:     "tok-abc",
		Printers: []Binding{
			{PrinterID: 3, Printer: usb("/dev/usb/lp0")},
			{PrinterID: 9, Printer: usb("/dev/usb/lp1")},
		},
		DrawerPin: 1,
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip lệch:\n got %+v\nmuốn %+v", *got, *want)
	}
}

// Ghép thêm máy in KHÔNG được đè lên máy in đã có, còn ghép lại chính máy in cũ
// thì phải đổi thiết bị tại chỗ. Trùng id sẽ khiến vòng lặp chính mở hai lần cùng
// một thiết bị.
func TestUpsertAddsAndReplaces(t *testing.T) {
	c := &Config{}
	c.Upsert(Binding{PrinterID: 3, Printer: usb("/dev/usb/lp0")})
	c.Upsert(Binding{PrinterID: 9, Printer: usb("/dev/usb/lp1")})
	if len(c.Printers) != 2 {
		t.Fatalf("muốn 2 máy in, có %d", len(c.Printers))
	}

	// Ghép lại máy in 3 sang thiết bị khác (cắm lại cổng USB khác).
	c.Upsert(Binding{PrinterID: 3, Printer: usb("/dev/usb/lp2")})
	if len(c.Printers) != 2 {
		t.Fatalf("ghép lại không được đẻ thêm mục: có %d", len(c.Printers))
	}
	if c.Printers[0].Printer.Target != "/dev/usb/lp2" {
		t.Errorf("thiết bị = %q, muốn /dev/usb/lp2", c.Printers[0].Printer.Target)
	}
	if c.Printers[1].PrinterID != 9 {
		t.Errorf("máy in kia bị xê dịch: %+v", c.Printers[1])
	}
}

// Config chứa token — không được để mọi tài khoản trên máy đọc.
func TestSaveUsesRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := &Config{
		ServerURL: "http://x",
		Token:     "t",
		Printers:  []Binding{{PrinterID: 1, Printer: usb("/dev/usb/lp0")}},
	}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("quyền tệp = %o, muốn 600 (tệp chứa token)", perm)
	}
}

// Ghi đè không được để lại tệp tạm — rác .tmp trong /etc là thứ gây hoang mang
// khi đi hỗ trợ.
func TestSaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := &Config{
		ServerURL: "http://x",
		Token:     "t",
		Printers:  []Binding{{PrinterID: 1, Printer: usb("/dev/usb/lp0")}},
	}

	for range 2 {
		if err := Save(path, c); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("còn sót tệp: %v", names)
	}
}

// Mỗi lỗi cấu hình phải nói ra HÀNH ĐỘNG, vì người đọc là kỹ thuật viên đang
// đứng ở quán chứ không phải lập trình viên đọc stack trace.
func TestLoadErrorsAreActionable(t *testing.T) {
	dir := t.TempDir()
	const oneUSB = `"printers":[{"printer_id":3,"printer":{"kind":"usb","target":"/dev/usb/lp0"}}]`

	tests := []struct {
		name    string
		write   string // "" = không tạo tệp
		wantHas string
	}{
		{"chưa ghép nối", "", "pair"},
		{"json hỏng", "{ hỏng", "hỏng định dạng"},
		{"thiếu token", `{"server_url":"http://x",` + oneUSB + `}`, "token"},
		{"chưa ghép máy in nào", `{"server_url":"http://x","token":"t"}`, "máy in"},
		{"thiếu địa chỉ ERP", `{"token":"t",` + oneUSB + `}`, "server_url"},
		{
			"máy in thiếu id",
			`{"server_url":"http://x","token":"t","printers":[{"printer":{"kind":"usb","target":"/dev/usb/lp0"}}]}`,
			"printer_id",
		},
		{
			"máy in chưa chọn thiết bị",
			`{"server_url":"http://x","token":"t","printers":[{"printer_id":3}]}`,
			"thiết bị",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".json")
			if tc.write != "" {
				if err := os.WriteFile(path, []byte(tc.write), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("muốn lỗi, nhận nil")
			}
			if !strings.Contains(err.Error(), tc.wantHas) {
				t.Errorf("thông báo %q không chứa %q", err.Error(), tc.wantHas)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Danh tính thiết bị (047f)
// ─────────────────────────────────────────────────────────────────────────────

func usbWithID(target string, id printer.Identity) printer.Config {
	c := usb(target)
	c.Identity = &id
	return c
}

// Cấu hình MỚI phải đọc được bằng bản binary CŨ và ngược lại.
//
// Đây là điều kiện để "hạ cấp binary" là đường lùi đủ: `identity` chỉ THÊM vào,
// `target` giữ nguyên. Không có tính chất này thì lùi bản ở quán nghĩa là phải
// sửa tệp bằng tay lúc 7 giờ sáng.
func TestDanhTinhChiThemKhongXoaTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := &Config{
		ServerURL: "http://192.168.1.50",
		Token:     "tok",
		Printers: []Binding{{
			PrinterID: 7,
			Printer: usbWithID("/dev/usb/lp0", printer.Identity{
				Serial: "1C79EC420000", CMD: "EPSON", IDs: "0483:5743", USBPath: "1-3", Model: "POS-80",
			}),
		}},
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"target": "/dev/usb/lp0"`) {
		t.Error("phải GIỮ target — nó là đường lùi cho binary cũ và manh mối tự chữa")
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip lệch:\n got %+v\nmuốn %+v", *got, *want)
	}
}

// Cấu hình đời cũ (chưa có identity) vẫn phải nạp được — 5 quán đang chạy nó.
func TestCauHinhDoiCuVanNapDuoc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{"server_url":"http://x","token":"t","printers":[{"printer_id":3,"printer":{"kind":"usb","target":"/dev/usb/lp2"}}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("cấu hình đời cũ phải nạp được: %v", err)
	}
	if got.Printers[0].Printer.Identity != nil {
		t.Error("chưa tự chữa thì identity phải là nil")
	}
	if got.Printers[0].DeviceKey() != "path:/dev/usb/lp2" {
		t.Errorf("chưa có danh tính thì khoá phải rơi về đường dẫn: %s", got.Printers[0].DeviceKey())
	}
}

// Khoá gom nhóm phải theo CHIẾC MÁY, không theo đường dẫn.
//
// Cùng một máy in mà hôm nay ở lp0, mai ở lp1 vẫn phải ra CÙNG một khoá — nếu
// không, cùng một thiết bị sẽ đẻ ra hai hàng đợi và hai hàng đợi ghi vào một
// cổng chính là lỗi mà việc gom hàng đợi sinh ra để chặn.
func TestDeviceKeyTheoDanhTinhKhongTheoDuongDan(t *testing.T) {
	id := printer.Identity{Serial: "1C79EC420000", CMD: "EPSON", IDs: "0483:5743", USBPath: "1-3"}
	homNay := Binding{PrinterID: 1, Printer: usbWithID("/dev/usb/lp0", id)}
	homSau := Binding{PrinterID: 2, Printer: usbWithID("/dev/usb/lp7", id)}

	if homNay.DeviceKey() != homSau.DeviceKey() {
		t.Errorf("đổi đường dẫn không được đổi khoá: %s ≠ %s", homNay.DeviceKey(), homSau.DeviceKey())
	}
}

// Hai máy in ERP dùng chung một thiết bị KHÔNG còn là lỗi.
//
// Ca dùng thật của quán: một máy POS-80 in cả bill lẫn phiếu bếp. Trước đây
// agent TỪ CHỐI khởi động với cấu hình này.
func TestDungChungThietBiLaCanhBaoChuKhongPhaiLoi(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	id := printer.Identity{Serial: "1C79EC420000", CMD: "EPSON", IDs: "0483:5743", USBPath: "1-3"}
	cfg := &Config{
		ServerURL: "http://x", Token: "t",
		Printers: []Binding{
			{PrinterID: 1, Printer: usbWithID("/dev/usb/lp0", id)}, // Bill quầy
			{PrinterID: 2, Printer: usbWithID("/dev/usb/lp0", id)}, // Phiếu bếp
		},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("dùng chung thiết bị KHÔNG được chặn agent khởi động: %v", err)
	}

	w := got.Warnings()
	if len(w) != 1 {
		t.Fatalf("muốn đúng 1 cảnh báo, nhận %d: %v", len(w), w)
	}
	for _, want := range []string{"id 1", "id 2", "CHUNG"} {
		if !strings.Contains(w[0], want) {
			t.Errorf("cảnh báo %q thiếu %q", w[0], want)
		}
	}
}

// Hai thiết bị khác nhau thì im lặng — cảnh báo thừa làm người ta quen bỏ qua.
func TestKhacThietBiThiKhongCanhBao(t *testing.T) {
	cfg := &Config{Printers: []Binding{
		{PrinterID: 1, Printer: usbWithID("/dev/usb/lp0", printer.Identity{Serial: "AAA"})},
		{PrinterID: 2, Printer: usbWithID("/dev/usb/lp1", printer.Identity{Serial: "BBB"})},
	}}
	if w := cfg.Warnings(); len(w) != 0 {
		t.Errorf("không nên cảnh báo gì: %v", w)
	}
}
