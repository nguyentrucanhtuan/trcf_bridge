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
