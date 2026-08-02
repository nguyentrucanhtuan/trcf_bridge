package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyentrucanhtuan/trcf_print_agent/internal/printer"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")

	want := &Config{
		ServerURL: "http://192.168.1.50",
		Token:     "tok-abc",
		PrinterID: 3,
		Printer:   printer.Config{Kind: printer.KindUSB, Target: "/dev/usb/lp0"},
		DrawerPin: 1,
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != *want {
		t.Fatalf("round-trip lệch:\n got %+v\nmuốn %+v", *got, *want)
	}
}

// Config chứa token — không được để mọi tài khoản trên máy đọc.
func TestSaveUsesRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, &Config{ServerURL: "http://x", Token: "t", Printer: printer.Config{Kind: "usb", Target: "/dev/usb/lp0"}}); err != nil {
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
	c := &Config{ServerURL: "http://x", Token: "t", Printer: printer.Config{Kind: "usb", Target: "/dev/usb/lp0"}}

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

	tests := []struct {
		name    string
		write   string // "" = không tạo tệp
		wantHas string
	}{
		{"chưa ghép nối", "", "pair"},
		{"json hỏng", "{ hỏng", "hỏng định dạng"},
		{"thiếu token", `{"server_url":"http://x","printer":{"kind":"usb","target":"/dev/usb/lp0"}}`, "token"},
		{"chưa chọn máy in", `{"server_url":"http://x","token":"t"}`, "máy in"},
		{"thiếu địa chỉ ERP", `{"token":"t","printer":{"kind":"usb","target":"/dev/usb/lp0"}}`, "server_url"},
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
