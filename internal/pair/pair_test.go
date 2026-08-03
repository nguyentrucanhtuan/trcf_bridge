package pair

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Lỗi 400 của ERP nay có NHIỀU nguyên nhân khác nhau (mã hết hạn · máy in đã bị xoá ·
// máy in đang do máy quầy khác còn sống cầm). Gộp tất cả thành một câu "lấy mã mới" thì
// người lắp sẽ lấy mã mới, ghép lại, hỏng y như cũ, và không bao giờ đoán ra vì sao.
func TestPairSurfacesServerReason(t *testing.T) {
	const reason = `Máy in "Bill quầy" đang do chương trình in ở máy "quay-01" cầm, và máy đó vẫn đang chạy.`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"statusCode":400,"message":` + quote(reason) + `,"error":"Bad Request"}`))
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL, Request{Code: "123456"}, "")
	if err == nil {
		t.Fatal("phải trả lỗi khi ERP từ chối")
	}
	if !strings.Contains(err.Error(), "quay-01") {
		t.Fatalf("câu lỗi phải nêu đích danh máy quầy đang cầm, nhận được: %q", err)
	}
}

// Thân phản hồi không phải JSON (proxy chen ngang, trang lỗi HTML) thì vẫn phải có câu
// hướng dẫn được — đập một đống HTML vào mặt người đang lắp máy in thì tệ hơn.
func TestPairFallsBackWhenNoServerMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL, Request{Code: "123456"}, "")
	if err == nil || !strings.Contains(err.Error(), "lấy mã mới") {
		t.Fatalf("thiếu câu dự phòng, nhận được: %v", err)
	}
}

func TestServerMessageAcceptsValidationArray(t *testing.T) {
	// ValidationPipe của NestJS gom nhiều lỗi thành MẢNG — không xử lý dạng này thì
	// đúng lúc DTO sai lại rơi về câu mặc định chẳng liên quan.
	got := serverMessage([]byte(`{"message":["code phải là chuỗi","code không được rỗng"]}`))
	if !strings.Contains(got, "không được rỗng") {
		t.Fatalf("mảng message không được đọc, nhận được: %q", got)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
