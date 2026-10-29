package core

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

// Máy in LAN giả nhận mọi thứ rồi đóng.
func sinkPrinter(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, c)
			c.Close()
		}
	}()
	return ln.Addr().String()
}

// ERP giả cho /bridge/pair: đếm số lần gọi và ghi lại token cũ agent gửi kèm.
func pairERP(t *testing.T, calls *int32, gotAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		*gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok-1", "printer_id": 5, "printer_name": "Bill quầy"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPairMayInHongThiKhongGoiERP(t *testing.T) {
	var calls int32
	var auth string
	srv := pairERP(t, &calls, &auth)

	// Cổng không ai nghe: in thử hỏng.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	_, _, err := Pair(context.Background(), PairParams{
		ConfigPath: cfgPath, Server: srv.URL, Code: "123456",
		Target: printer.Config{Kind: printer.KindNet, Target: dead}, OS: "android", Version: "t",
	}, nil)
	if err == nil {
		t.Fatal("máy in hỏng mà vẫn ghép được")
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("máy in hỏng mà vẫn gọi ERP %d lần — ERP đã có bản ghi rác", n)
	}
}

func TestPairLuuTokenVaGanThemMayIn(t *testing.T) {
	var calls int32
	var auth string
	srv := pairERP(t, &calls, &auth)
	addr := sinkPrinter(t)

	// Máy này đã ghép trước đó: phải gửi kèm token cũ để ERP GẮN THÊM, không đẻ máy quầy mới.
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(cfgPath, &config.Config{
		ServerURL: srv.URL, Token: "tok-old", DrawerPin: 1,
		Printers: []config.Binding{{PrinterID: 2, Printer: printer.Config{Kind: printer.KindNet, Target: addr}}},
	}); err != nil {
		t.Fatal(err)
	}

	res, cfg, err := Pair(context.Background(), PairParams{
		ConfigPath: cfgPath, Server: srv.URL + "/", Code: " 123456 ",
		Target: printer.Config{Kind: printer.KindNet, Target: addr}, OS: "android", Version: "t",
	}, nil)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if auth != "Bearer tok-old" {
		t.Fatalf("không gửi kèm token cũ (Authorization=%q)", auth)
	}
	if res.PrinterID != 5 || cfg.Token != "tok-1" || len(cfg.Printers) != 2 {
		t.Fatalf("kết quả sai: res=%+v token=%q printers=%d", res, cfg.Token, len(cfg.Printers))
	}
	if cfg.DrawerPin != 1 {
		t.Fatalf("không chọn chân két mà chân két bị ghi đè thành %d", cfg.DrawerPin)
	}

	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token != "tok-1" || saved.ServerURL != srv.URL || len(saved.Printers) != 2 {
		t.Fatalf("tệp cấu hình không khớp: %+v", saved)
	}
}
