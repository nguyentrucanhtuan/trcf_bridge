package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/transport"
)

// Phần KEO giữa "ERP đổi bản gán" và "agent chạy theo bản mới": nhịp sống → thấy rev
// lệch → tải bản gán → ghi tệp. Kiểm bằng máy chủ HTTP THẬT + tệp THẬT, vì đây là chỗ
// hai thế giới gặp nhau và mọi giả lập ở đây đều làm mất đúng thứ cần đo.
//
// Không cần máy in nào: `syncBindings` chỉ đụng mạng và đĩa.

func writeConfig(t *testing.T, c *config.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return path
}

func erpStub(t *testing.T, rev int, binds []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bridge/bindings" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"bindings_rev": rev, "bindings": binds})
	}))
	t.Cleanup(srv.Close)
	return srv
}

const keyBillE2E = "usb://POS-80?cmd=EPSON&ids=0483%3A5743&serial=1C79EC420000"

// Đường chính: quản lý bấm Lưu trên web → agent ghi bản gán mới xuống tệp.
func TestSyncBindingsGhiBanGanMoiXuongTep(t *testing.T) {
	srv := erpStub(t, 3, []map[string]any{
		{"printer_id": 7, "device_key": keyBillE2E, "printer_name": "Bill quầy"},
	})
	path := writeConfig(t, &config.Config{
		ServerURL: srv.URL,
		Token:     "tok",
		Printers:  []config.Binding{{PrinterID: 7, Printer: printer.Config{Kind: printer.KindUSB, Target: "/dev/usb/lp2"}}},
	})

	changed, err := syncBindings(context.Background(), transport.New(srv.URL, "tok", "test"), path)
	if err != nil {
		t.Fatalf("syncBindings: %v", err)
	}
	if !changed {
		t.Fatal("phải báo có thay đổi để phiên nạp lại")
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	id := got.Printers[0].Printer.Identity
	if id == nil || id.Serial != "1C79EC420000" {
		t.Fatalf("chưa ghi danh tính mới xuống tệp: %+v", got.Printers[0].Printer)
	}
	// `target` cũ phải còn: hạ binary về bản trước 047f vẫn phải in được.
	if got.Printers[0].Printer.Target != "/dev/usb/lp2" {
		t.Errorf("mất target cũ → hết đường lùi: %q", got.Printers[0].Printer.Target)
	}
}

// Không có gì đổi thì KHÔNG ghi tệp và KHÔNG nạp lại.
//
// Quan trọng vì `changed` là thứ kích hoạt rút lui + dựng lại thợ in: báo bừa thì cứ
// 30 giây quán lại có một khoảnh khắc không ai in được.
func TestSyncBindingsKhongDoiThiKhongGhi(t *testing.T) {
	srv := erpStub(t, 3, []map[string]any{{"printer_id": 7, "device_key": keyBillE2E}})
	id, _ := printer.ParseIdentity(keyBillE2E)
	path := writeConfig(t, &config.Config{
		ServerURL: srv.URL,
		Token:     "tok",
		Printers:  []config.Binding{{PrinterID: 7, Printer: printer.Config{Kind: printer.KindUSB, Identity: &id}}},
	})
	before, _ := os.ReadFile(path)

	changed, err := syncBindings(context.Background(), transport.New(srv.URL, "tok", "test"), path)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v — không đổi thì phải im", changed, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("đã ghi lại tệp dù không có gì đổi")
	}
}

// ERP bản CŨ chưa có đường `/bridge/bindings` → agent chạy tiếp theo cấu hình cục bộ,
// không coi là lỗi. Thiếu tính chất này thì nâng ERP và nâng agent buộc phải cùng lúc.
func TestSyncBindingsERPBanCuKhongPhaiLoi(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	path := writeConfig(t, &config.Config{ServerURL: srv.URL, Token: "tok"})

	changed, err := syncBindings(context.Background(), transport.New(srv.URL, "tok", "test"), path)
	if err != nil {
		t.Fatalf("ERP bản cũ không được coi là lỗi: %v", err)
	}
	if changed {
		t.Error("không có gì để áp thì đừng nạp lại")
	}
}

// 🎯 ERP trả danh sách RỖNG (dấu hiệu lỗi phía kia hơn là ý định thật) → giữ nguyên
// cấu hình đang chạy. Xoá nhầm ở đây là cả quán ngừng in mà agent vẫn báo xanh.
func TestSyncBindingsRongThiGiuNguyenDuongIn(t *testing.T) {
	srv := erpStub(t, 9, []map[string]any{})
	id, _ := printer.ParseIdentity(keyBillE2E)
	path := writeConfig(t, &config.Config{
		ServerURL: srv.URL,
		Token:     "tok",
		Printers:  []config.Binding{{PrinterID: 7, Printer: printer.Config{Kind: printer.KindUSB, Identity: &id}}},
	})

	changed, err := syncBindings(context.Background(), transport.New(srv.URL, "tok", "test"), path)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, _ := config.Load(path)
	if len(got.Printers) != 1 {
		t.Fatalf("ĐÃ XOÁ đường in của quán: %+v", got.Printers)
	}
}

// Nhịp sống mang theo kiểm kê và mang về số bản gán — cả hai đi nhờ một request.
func TestHeartbeatGuiKiemKeVaNhanBindingsRev(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "bindings_rev": 12})
	}))
	defer srv.Close()

	rev, err := transport.New(srv.URL, "tok", "test").Heartbeat(
		context.Background(),
		[]transport.PrinterStatus{{PrinterID: 7, PortOK: true}},
		[]transport.DeviceInfo{{DeviceKey: keyBillE2E, Model: "POS-80", CMD: "EPSON"}},
	)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if rev != 12 {
		t.Errorf("bindings_rev = %d", rev)
	}
	devs, _ := body["devices"].([]any)
	if len(devs) != 1 {
		t.Fatalf("kiểm kê không tới nơi: %v", body["devices"])
	}
}

// 🎯 `nil` ≠ `[]`: chưa dò được thì TUYỆT ĐỐI không gửi mảng rỗng, vì ERP đọc mảng rỗng
// thành "quán không cắm máy in nào" và xoá sạch ô chọn của người dùng.
func TestHeartbeatChuaDoDuocThiKhongGuiTruongDevices(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	if _, err := transport.New(srv.URL, "tok", "test").Heartbeat(context.Background(), nil, nil); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if _, has := body["devices"]; has {
		t.Errorf("đã gửi devices dù chưa dò được: %v", body["devices"])
	}
}

// ERP bản cũ trả `{"ok":true}` → rev 0 → agent hiểu là "không có gì để đồng bộ".
func TestHeartbeatERPBanCuTraRevKhong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	rev, err := transport.New(srv.URL, "tok", "test").Heartbeat(context.Background(), nil, nil)
	if err != nil || rev != 0 {
		t.Fatalf("rev=%d err=%v", rev, err)
	}
}
