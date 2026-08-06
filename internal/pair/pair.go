// Package pair đổi mã ghép 6 số lấy token dài hạn.
//
// Vì sao dùng mã ngắn hạn thay vì bắt người lắp dán token: token dài, dễ gõ sai,
// và nếu lộ thì dùng được mãi. Mã 6 số hạn 10 phút thì đọc qua điện thoại được,
// và hết hạn thì vô dụng.
package pair

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Result — thứ ERP trả về sau khi nhận mã hợp lệ.
type Result struct {
	Token     string `json:"token"`
	PrinterID int    `json:"printer_id"`
	// PrinterName để in ra màn hình cho người lắp XÁC NHẬN mình vừa ghép đúng
	// máy in nào — quán có 2–3 máy in thì rất dễ ghép nhầm.
	PrinterName string `json:"printer_name"`
}

// Request gửi lên kèm mô tả máy in vật lý đã chọn, để ERP hiện được ở màn
// `/printers` (vd "Xprinter XP-80C (/dev/usb/lp0)").
type Request struct {
	Code        string `json:"code"`
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	PrinterDesc string `json:"printer_desc"`
	Version     string `json:"version"`
}

// Do gọi ERP đổi mã lấy quyền in cho một máy in.
//
// `existingToken` là token đang có trong cấu hình của máy này, để trống nếu chưa
// ghép lần nào. Gửi kèm thì ERP GẮN THÊM máy in vào chính máy quầy đó và trả lại
// đúng token cũ — nhờ vậy một máy tính cầm 2–3 máy in mà vẫn chỉ một tiến trình,
// một dịch vụ nền, một tệp cấu hình. Token sai hoặc đã bị thu hồi thì ERP dựng
// máy quầy mới, không báo lỗi: người lắp không có cách nào tự gỡ nếu bị từ chối.
func Do(ctx context.Context, baseURL string, req Request, existingToken string) (*Result, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(baseURL, "/") + "/bridge/pair"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "trcf-bridge/"+req.Version)
	if existingToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+existingToken)
	}

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("không gọi được ERP tại %s — kiểm tra địa chỉ và mạng: %w", baseURL, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var r Result
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("ERP trả dữ liệu ghép nối không đọc được: %w", err)
		}
		if r.Token == "" {
			return nil, fmt.Errorf("ERP không cấp token — kiểm tra lại phía máy chủ")
		}
		return &r, nil
	case http.StatusNotFound, http.StatusGone, http.StatusBadRequest:
		// ERP viết sẵn câu tiếng Việt cho từng lý do (mã hết hạn · máy in đã bị xoá ·
		// máy in đang do máy quầy khác còn sống cầm) — HIỆN NGUYÊN VĂN.
		//
		// Trước đây ba mã này gộp thành một câu "mã ghép sai, lấy mã mới" vì lúc đó chỉ
		// có đúng một nguyên nhân. Nay thì không: người lắp gặp máy in đang thuộc máy
		// khác mà được bảo đi lấy mã mới sẽ lấy mã mới, ghép lại, hỏng y như cũ, và
		// không bao giờ đoán ra nguyên nhân thật.
		if msg := serverMessage(body); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, fmt.Errorf("mã ghép sai hoặc đã hết hạn — vào ERP mục Máy in bấm lấy mã mới")
	default:
		return nil, fmt.Errorf("ERP trả mã %d khi ghép nối: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// serverMessage rút câu giải thích ERP gửi kèm lỗi.
//
// NestJS trả `message` là chuỗi, HOẶC mảng chuỗi khi ValidationPipe gom nhiều lỗi một
// lúc — không xử lý cả hai dạng thì đúng lúc cần nhất lại rơi về câu mặc định. Thân
// phản hồi không đọc được thì trả rỗng để nơi gọi dùng câu dự phòng: một dòng JSON thô
// đập vào mặt người đang đứng lắp máy in thì tệ hơn là một câu chung chung.
func serverMessage(body []byte) string {
	var one struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &one); err == nil && strings.TrimSpace(one.Message) != "" {
		return strings.TrimSpace(one.Message)
	}
	var many struct {
		Message []string `json:"message"`
	}
	if err := json.Unmarshal(body, &many); err == nil && len(many.Message) > 0 {
		return strings.TrimSpace(strings.Join(many.Message, "; "))
	}
	return ""
}
