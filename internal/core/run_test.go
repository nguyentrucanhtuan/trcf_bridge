package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

// Trọn một vòng `Run` — đúng đường mà cả Linux (`run`) lẫn Android (Foreground
// Service) đi: nạp cấu hình → long-poll nhận lệnh in → thợ của thiết bị ghi ra máy
// in → báo kết quả về ERP → huỷ ctx thì dừng gọn. Máy in là cổng TCP giả nên chạy
// được trên mọi hệ điều hành.
func TestRunInMotLenhRoiBaoKetQua(t *testing.T) {
	// Máy in LAN giả: gom mọi byte nhận được.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	// Máy in giả đọc ở goroutine riêng: agent ghi xong (byte đã vào socket) rồi báo
	// ERP có thể TRƯỚC khi goroutine này gom xong — nên test chờ kênh này, không đọc
	// một biến dùng chung ngay khi ERP nhận kết quả.
	printed := make(chan []byte, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(c)
			c.Close()
			if len(b) > 0 { // bỏ qua cú thăm cổng rỗng của nhịp sống
				printed <- b
			}
		}
	}()

	bill := []byte("\x1b@HOA DON 42\x1dV\x00")
	results := make(chan map[string]any, 1)
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/bridge/jobs/next":
			sent := false
			once.Do(func() {
				sent = true
				_ = json.NewEncoder(w).Encode(map[string]any{
					"job_id": 42, "printer_id": 7, "format": "escpos",
					"data": base64.StdEncoding.EncodeToString(bill),
				})
			})
			if !sent {
				// Giữ long-poll như ERP thật thay vì trả 204 dồn dập.
				select {
				case <-r.Context().Done():
				case <-time.After(200 * time.Millisecond):
				}
				w.WriteHeader(http.StatusNoContent)
			}
		case "/bridge/jobs/42/result":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			select {
			case results <- body:
			default:
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	cfgPath := writeConfig(t, &config.Config{
		ServerURL: srv.URL,
		Token:     "tok",
		Printers: []config.Binding{{
			PrinterID: 7,
			Printer:   printer.Config{Kind: printer.KindNet, Target: ln.Addr().String()},
		}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfgPath, "test") }()

	select {
	case body := <-results:
		if body["ok"] != true {
			t.Fatalf("ERP nhận kết quả hỏng: %v", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("quá 10 giây mà ERP chưa nhận kết quả lệnh in")
	}

	select {
	case got := <-printed:
		if string(got) != string(bill) {
			t.Fatalf("máy in nhận %q, muốn %q", got, bill)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ERP đã nhận kết quả \"ok\" mà máy in không nhận byte nào")
	}

	// Cho agent đọc xong phản hồi 200 trước khi huỷ — huỷ ngay thì nhật ký hiện một
	// lỗi "không báo được kết quả" giả (kết luận của test không đổi).
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run trả lỗi khi dừng: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("huỷ ctx mà Run không dừng")
	}
}
