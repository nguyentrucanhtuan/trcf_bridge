package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		if !strings.HasSuffix(r.URL.Path, "/print-agent/jobs/42/result") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "test")
	if err := c.ReportResult(context.Background(), 42, errors.New("hết giấy")); err != nil {
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
}

func TestReportResultSuccessOmitsError(t *testing.T) {
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New(srv.URL, "tok", "test").ReportResult(context.Background(), 1, nil); err != nil {
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
// đường dẫn "//print-agent/..." khiến proxy trả 404.
func TestBaseURLTrailingSlashNormalized(t *testing.T) {
	var path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, _ = New(srv.URL+"/", "tok", "test").NextJob(context.Background())
	if path != "/print-agent/jobs/next" {
		t.Errorf("path = %q, muốn /print-agent/jobs/next", path)
	}
}
