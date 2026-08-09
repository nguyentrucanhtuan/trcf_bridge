package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNextJobDecodesPayload(t *testing.T) {
	want := []byte{0x1B, 0x40, 'A', 'B'}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok123" {
			t.Errorf("Authorization = %q, muốn Bearer tok123", got)
		}
		if got := r.URL.Query().Get("wait"); got != "25" {
			t.Errorf("wait = %q, muốn 25", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Job{
			ID: 7, Format: "escpos",
			Data: base64.StdEncoding.EncodeToString(want),
		})
	}))
	defer srv.Close()

	job, err := New(srv.URL, "tok123", "test").NextJob(context.Background())
	if err != nil {
		t.Fatalf("NextJob: %v", err)
	}
	if job.ID != 7 {
		t.Errorf("job.ID = %d, muốn 7", job.ID)
	}

	got, err := job.Payload()
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("payload = % X, muốn % X", got, want)
	}
}

// 204 = "chưa có việc" là trạng thái BÌNH THƯỜNG (quán vắng), phải phân biệt
// được với lỗi thật — nhầm chỗ này thì agent sẽ backoff tới 60s và bill của
// khách kế tiếp ra chậm cả phút.
func TestNextJobNoContentIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok", "test").NextJob(context.Background())
	if !errors.Is(err, ErrNoJob) {
		t.Fatalf("muốn ErrNoJob, nhận %v", err)
	}
}

// Token bị thu hồi thì thông báo phải chỉ ra việc cần làm (`pair` lại), vì retry
// bao lâu cũng vô ích.
func TestNextJobRejectedTokenTellsUserToPair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok", "test").NextJob(context.Background())
	if err == nil {
		t.Fatal("muốn lỗi khi ERP từ chối token")
	}
	if errors.Is(err, ErrNoJob) {
		t.Fatal("401 bị nhầm thành 'chưa có việc' — agent sẽ quay vòng vô ích")
	}
	if !strings.Contains(err.Error(), "pair") {
		t.Errorf("thông báo lỗi không chỉ ra cách xử lý: %v", err)
	}
}

func TestReportResultSendsError(t *testing.T) {
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, muốn POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/bridge/jobs/42/result") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "test")
	if err := c.ReportResult(context.Background(), 42, errors.New("hết giấy"), false); err != nil {
		t.Fatalf("ReportResult: %v", err)
	}

	if body["ok"] != false {
		t.Errorf("ok = %v, muốn false", body["ok"])
	}
	// Lý do hỏng phải đi kèm: ERP hiện đúng nguyên nhân ở quầy thì nhân viên mới
	// biết thay giấy thay vì gọi kỹ thuật.
	if body["error"] != "hết giấy" {
		t.Errorf("error = %v, muốn \"hết giấy\"", body["error"])
	}
	// Chưa chạm giấy thì KHÔNG gắn cờ — để ERP giữ nguyên thang thử lại.
	if _, ok := body["may_have_printed"]; ok {
		t.Errorf("lỗi chưa chạm giấy mà vẫn gắn may_have_printed: %v", body)
	}
}

// Cờ này là thứ chặn ERP tự in lại (sự cố Gác Đờ Rô 2026-08-09). Mất nó trên đường
// truyền thì mọi lỗi giữa chừng lại đẻ ra tờ thứ hai.
func TestReportResultGuiCoDaChamGiay(t *testing.T) {
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := New(srv.URL, "tok", "test").
		ReportResult(context.Background(), 7, errors.New("không nhận hết dữ liệu"), true)
	if err != nil {
		t.Fatalf("ReportResult: %v", err)
	}
	if body["may_have_printed"] != true {
		t.Errorf("thiếu cờ may_have_printed: %v", body)
	}
}

// Một cú POST hụt từng đủ để đẻ ra một tờ bill thừa: job nằm `sent`, bị thu hồi rồi
// giao lại. Thử lại vài lượt là cách rẻ nhất để bịt.
func TestReportResultThuLaiKhiERPTraLoi5xx(t *testing.T) {
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "test")
	c.reportWait = time.Millisecond // không ai chờ 2 giây trong test

	if err := c.ReportResult(context.Background(), 9, nil, false); err != nil {
		t.Fatalf("phải thử lại tới khi ERP tỉnh: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("gọi %d lần, muốn 3 (2 lần hỏng + 1 lần được)", got)
	}
}

// ERP bản CŨ bật `forbidNonWhitelisted` sẽ trả 400 vì không biết trường mới. Bỏ
// trường rồi báo lại — thà mất một chi tiết còn hơn để job kẹt `sent`, vì kẹt
// `sent` chính là đường đẻ ra tờ thứ hai.
func TestReportResultBoCoMoiKhiERPCuTuChoi(t *testing.T) {
	var bodies []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if _, ok := b["may_have_printed"]; ok {
			w.WriteHeader(http.StatusBadRequest) // ERP đời cũ
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "test")
	c.reportWait = time.Millisecond

	if err := c.ReportResult(context.Background(), 5, errors.New("kẹt"), true); err != nil {
		t.Fatalf("phải báo lại được sau khi bỏ trường lạ: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("gọi %d lần, muốn 2 (1 lần có cờ bị từ chối + 1 lần bỏ cờ)", len(bodies))
	}
	if bodies[1]["error"] != "kẹt" {
		t.Errorf("lượt bỏ cờ vẫn phải giữ câu lỗi: %v", bodies[1])
	}
}

// 4xx khác (job không tồn tại, token hỏng) thì thử lại vô ích — dừng ngay.
func TestReportResultKhongThuLaiVoiLoi4xx(t *testing.T) {
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "test")
	c.reportWait = time.Millisecond

	if err := c.ReportResult(context.Background(), 3, nil, false); err == nil {
		t.Fatal("muốn lỗi khi ERP trả 404")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("gọi %d lần, muốn 1 — 404 thì thử lại vô ích", got)
	}
}

func TestReportResultSuccessOmitsError(t *testing.T) {
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New(srv.URL, "tok", "test").ReportResult(context.Background(), 1, nil, false); err != nil {
		t.Fatalf("ReportResult: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, muốn true", body["ok"])
	}
	if _, has := body["error"]; has {
		t.Errorf("in thành công mà vẫn gửi kèm error: %v", body["error"])
	}
}

// URL có dấu / ở cuối là lỗi gõ rất hay gặp lúc lắp đặt; nó không được đẻ ra
// đường dẫn "//bridge/..." khiến proxy trả 404.
func TestBaseURLTrailingSlashNormalized(t *testing.T) {
	var path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, _ = New(srv.URL+"/", "tok", "test").NextJob(context.Background())
	if path != "/bridge/jobs/next" {
		t.Errorf("path = %q, muốn /bridge/jobs/next", path)
	}
}

// 🎯 ERP bản trước 047f bật `forbidNonWhitelisted`, nên trường `devices` lạ làm CẢ
// gói nhịp sống rơi 400 — đo thật ở coffeetreeroaster 2026-08-08:
//
//	{"message":["property devices should not exist"],"statusCode":400}
//
// Hậu quả nếu để nguyên KHÔNG phải agent chết, mà tệ hơn: nhịp hỏi việc vẫn làm
// `last_seen_at` tươi nên màn hình báo "máy quầy đang chạy · máy in tốt" trong khi
// trạng thái cổng đứng hình ở lần báo cuối. Đúng kiểu màn hình nói dối.
func TestHeartbeatERPCuTuChoiKiemKeThiGuiLaiKhongKemTheo(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if _, has := b["devices"]; has { // đúng hành vi ERP cũ
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":["property devices should not exist"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "test")
	devs := []DeviceInfo{{DeviceKey: "usb://POS-80?serial=X", Model: "POS-80"}}

	// Nhịp sống PHẢI thành công — trạng thái cổng quan trọng hơn ô chọn thiết bị.
	if _, err := c.Heartbeat(context.Background(), []PrinterStatus{{PrinterID: 1, PortOK: true}}, devs); err != nil {
		t.Fatalf("nhịp sống không được hỏng chỉ vì ERP chưa biết kiểm kê: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("muốn 2 lần gọi (kèm rồi bỏ kiểm kê), nhận %d", len(bodies))
	}
	if _, has := bodies[1]["devices"]; has {
		t.Error("lần gửi lại vẫn kèm devices — sẽ 400 tiếp")
	}
	if bodies[1]["printers"] == nil {
		t.Error("gửi lại phải GIỮ trạng thái cổng, đó mới là thứ không được mất")
	}
}

// Sau khi bị từ chối thì NHỊN, không nện ERP cũ hai request mỗi nhịp.
func TestHeartbeatNhinKiemKeSauKhiBiTuChoi(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if _, has := b["devices"]; has {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "test")
	devs := []DeviceInfo{{DeviceKey: "usb://X"}}
	for i := 0; i < 5; i++ {
		if _, err := c.Heartbeat(context.Background(), nil, devs); err != nil {
			t.Fatalf("nhịp %d hỏng: %v", i, err)
		}
	}
	// Nhịp 1 tốn 2 request (thử + gửi lại), 4 nhịp sau mỗi nhịp 1 request.
	if calls != 6 {
		t.Errorf("muốn 6 request, nhận %d — nhịn không có tác dụng", calls)
	}
}

// Nhịn có HẠN: ngày ERP được nâng cấp thì agent phải tự bật kiểm kê lên lại, không
// đợi ai ra quán khởi động dịch vụ.
func TestHeartbeatThuLaiKiemKeSauKyNhin(t *testing.T) {
	erpMoi := false
	kemKiemKe := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if _, has := b["devices"]; has {
			kemKiemKe++
			if !erpMoi {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		_, _ = w.Write([]byte(`{"ok":true,"bindings_rev":7}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "test")
	devs := []DeviceInfo{{DeviceKey: "usb://X"}}
	_, _ = c.Heartbeat(context.Background(), nil, devs) // bị từ chối → vào kỳ nhịn
	erpMoi = true                                       // quán được nâng cấp ERP

	var rev int
	for i := 0; i < devicesRetryEvery+1; i++ {
		rev, _ = c.Heartbeat(context.Background(), nil, devs)
	}
	if kemKiemKe < 2 {
		t.Fatalf("chưa thử lại kiểm kê sau kỳ nhịn (%d lần kèm)", kemKiemKe)
	}
	if rev != 7 {
		t.Errorf("ERP mới rồi mà chưa nhận được bindings_rev: %d", rev)
	}
}

// Lỗi 400 KHÔNG kèm kiểm kê là lỗi thật — đừng nuốt nó thành im lặng.
func TestHeartbeat400KhongKemKiemKeVanLaLoi(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "tok", "test").Heartbeat(context.Background(), nil, nil); err == nil {
		t.Error("400 không liên quan kiểm kê thì phải báo lỗi")
	}
}
