// Package config đọc/ghi tệp cấu hình duy nhất của agent.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nguyentrucanhtuan/trcf_print_agent/internal/printer"
)

// DefaultPath — nơi đặt config khi chạy như dịch vụ hệ thống trên Linux.
const DefaultPath = "/etc/trcf-print-agent/config.json"

// Config — toàn bộ thứ agent cần biết. Cố ý nhỏ: mọi thứ khác (mẫu bill, định
// tuyến, nội dung) nằm ở backend.
type Config struct {
	// ServerURL — gốc địa chỉ ERP, vd "http://192.168.1.50" hoặc
	// "https://erp.quan.vn". Không kèm đường dẫn.
	ServerURL string `json:"server_url"`

	// Token — cấp lúc ghép nối, gắn với ĐÚNG MỘT máy in. Mất token thì ghép lại,
	// không phải cài lại.
	Token string `json:"token"`

	// PrinterID — id máy in bên ERP, gửi kèm heartbeat để ERP biết ai đang sống.
	PrinterID int `json:"printer_id"`

	// Printer — máy in vật lý ở máy này.
	Printer printer.Config `json:"printer"`

	// DrawerPin — chân RJ11 đá két: 0 = chân 2 (đa số), 1 = chân 5.
	DrawerPin int `json:"drawer_pin"`
}

// Load đọc config. Lỗi ở đây phải nói rõ PHẢI LÀM GÌ, vì người đọc nó là kỹ
// thuật viên đang đứng ở quán lúc 7 giờ sáng, không phải lập trình viên.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("chưa ghép nối máy in (không thấy %s) — chạy `trcf-print-agent pair` trước", path)
		}
		return nil, fmt.Errorf("đọc %s: %w", path, err)
	}

	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("tệp cấu hình %s hỏng định dạng: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("cấu hình %s không hợp lệ: %w", path, err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.ServerURL == "" {
		return fmt.Errorf("thiếu server_url")
	}
	if c.Token == "" {
		return fmt.Errorf("thiếu token — chạy `pair` để ghép nối lại")
	}
	if c.Printer.Kind == "" || c.Printer.Target == "" {
		return fmt.Errorf("chưa chọn máy in (printer.kind/printer.target rỗng)")
	}
	return nil
}

// Save ghi config với quyền 0600 vì nó chứa token.
//
// Ghi qua tệp tạm rồi rename: rename trên cùng filesystem là thao tác nguyên
// tử, nên mất điện giữa chừng sẽ để lại config CŨ còn nguyên, không để lại một
// tệp cụt khiến agent không khởi động được nữa.
func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("tạo thư mục cấu hình: %w", err)
	}

	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("ghi tạm %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("thay tệp cấu hình %s: %w", path, err)
	}
	return nil
}
