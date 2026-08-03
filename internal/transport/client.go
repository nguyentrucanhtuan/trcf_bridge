// Package transport nói chuyện với ERP.
//
// Agent LUÔN gọi ra, ERP không bao giờ gọi vào (PRD FR-047.8). Nhờ vậy máy quầy
// không phải mở cổng, không cần IP tĩnh, không vướng NAT/firewall — và khi
// backend dời lên cloud thì không phải đổi gì ở quán.
package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PollWait — thời gian ERP giữ request khi chưa có việc. 25s: đủ dài để không
// nện request liên tục, đủ ngắn để lọt dưới mọi hạn nghỉ mặc định (proxy, LB,
// Caddy) vốn thường là 30–60s.
const PollWait = 25 * time.Second

// pollTimeout để dư 10s so với PollWait: server đáng ra trả lời ở giây 25, dư
// ra là để chịu độ trễ mạng — hết 35s mà im thì coi như đứt thật.
const pollTimeout = PollWait + 10*time.Second

// Job — một lần in.
type Job struct {
	ID int64 `json:"job_id"`
	// PrinterID — máy in nào phải nhận. Máy quầy cầm nhiều máy in nên đây là thứ
	// duy nhất phân biệt tờ bill với phiếu bếp.
	PrinterID int    `json:"printer_id"`
	Format    string `json:"format"` // hiện chỉ "escpos"
	Data      string `json:"data"`   // base64 của byte ĐÃ DỰNG SẴN ở backend
}

// Payload giải mã data. Agent chỉ giải base64 — KHÔNG diễn giải nội dung.
func (j Job) Payload() ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(j.Data)
	if err != nil {
		return nil, fmt.Errorf("job %d: dữ liệu in hỏng (base64): %w", j.ID, err)
	}
	return b, nil
}

// PrinterStatus — trạng thái cổng của một máy in, gửi kèm heartbeat.
type PrinterStatus struct {
	PrinterID int    `json:"printer_id"`
	PortOK    bool   `json:"port_ok"`
	LastErr   string `json:"last_err,omitempty"`
}

// Client — một agent (một MÁY TÍNH), nhiều máy in, một token.
type Client struct {
	baseURL string
	token   string
	version string

	// Hai http.Client tách nhau: long-poll cần hạn dài, còn báo kết quả và
	// heartbeat phải hỏng nhanh để không kẹt vòng lặp chính.
	pollHTTP  *http.Client
	shortHTTP *http.Client
}

func New(baseURL, token, version string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		version:   version,
		pollHTTP:  &http.Client{Timeout: pollTimeout},
		shortHTTP: &http.Client{Timeout: 10 * time.Second},
	}
}

// ErrNoJob — hết thời gian chờ mà không có việc. Đây là trạng thái BÌNH THƯỜNG
// (quán vắng khách), không phải lỗi: vòng lặp gọi lại ngay, không backoff.
var ErrNoJob = fmt.Errorf("chưa có việc in")

// NextJob chờ việc. Trả ErrNoJob khi hết giờ chờ.
func (c *Client) NextJob(ctx context.Context) (*Job, error) {
	url := fmt.Sprintf("%s/print-agent/jobs/next?wait=%d", c.baseURL, int(PollWait.Seconds()))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)

	resp, err := c.pollHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không gọi được ERP: %w", err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, ErrNoJob
	case http.StatusOK:
		var j Job
		if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
			return nil, fmt.Errorf("đọc việc in từ ERP: %w", err)
		}
		return &j, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		// Token bị thu hồi hoặc máy in đã xoá bên ERP. Đây KHÔNG phải lỗi mạng
		// tạm thời — retry mãi cũng vô ích, phải báo người vận hành ghép lại.
		return nil, fmt.Errorf("ERP từ chối token (%d) — máy in có thể đã bị xoá hoặc thu hồi; chạy `pair` lại", resp.StatusCode)
	default:
		return nil, fmt.Errorf("ERP trả mã %d khi lấy việc in", resp.StatusCode)
	}
}

// ReportResult báo kết quả in.
//
// Đây là bước KHÔNG ĐƯỢC BỎ: ERP dựa vào nó để đánh dấu job xong hay hỏng, và
// để hiện "phiếu bếp chưa in được" ở quầy (PRD FR-047.16). In được mà không báo
// thì ERP tưởng vẫn hỏng và sẽ in lại — ra hai tờ.
func (c *Client) ReportResult(ctx context.Context, jobID int64, printErr error) error {
	body := map[string]any{"ok": printErr == nil}
	if printErr != nil {
		body["error"] = printErr.Error()
	}
	b, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/print-agent/jobs/%d/result", c.baseURL, jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.shortHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("báo kết quả job %d: %w", jobID, err)
	}
	defer drain(resp)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("báo kết quả job %d: ERP trả mã %d", jobID, resp.StatusCode)
	}
	return nil
}

// Heartbeat cho ERP biết máy quầy còn sống và TỪNG máy in có mở cổng được không
// — nguồn dữ liệu cho cột trạng thái ở màn `/printers` (PRD FR-047.14).
//
// Gửi cả danh sách thay vì một cờ chung: các máy in trên cùng một máy tính hỏng
// độc lập, gộp lại thì rút dây máy in bếp sẽ kéo máy in bill báo đỏ theo và nhân
// viên đi kiểm nhầm chỗ.
func (c *Client) Heartbeat(ctx context.Context, printers []PrinterStatus) error {
	b, _ := json.Marshal(map[string]any{
		"version":  c.version,
		"printers": printers,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/print-agent/heartbeat", bytes.NewReader(b))
	if err != nil {
		return err
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.shortHTTP.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat: ERP trả mã %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "trcf-print-agent/"+c.version)
}

// drain đọc nốt body rồi đóng, để kết nối được tái dùng thay vì bị vứt —
// agent gọi endpoint này mỗi 25 giây suốt ngày.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}
