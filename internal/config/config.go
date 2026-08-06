// Package config đọc/ghi tệp cấu hình duy nhất của agent.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
)

// DefaultPath — nơi đặt config khi chạy như dịch vụ hệ thống trên Linux.
const DefaultPath = "/etc/trcf-bridge/config.json"

// Binding — một máy in bên ERP ứng với một thiết bị vật lý ở máy này.
type Binding struct {
	// PrinterID — id máy in bên ERP. Lệnh in gửi về mang theo số này, agent tra
	// ra thiết bị tương ứng để ghi. Sai chỗ này thì bill chui ra máy in bếp.
	PrinterID int `json:"printer_id"`

	// Printer — thiết bị vật lý ở máy này (device node hoặc IP:port).
	Printer printer.Config `json:"printer"`
}

// Config — toàn bộ thứ agent cần biết. Cố ý nhỏ: mọi thứ khác (mẫu bill, định
// tuyến, nội dung) nằm ở backend.
type Config struct {
	// ServerURL — gốc địa chỉ ERP, vd "http://192.168.1.50" hoặc
	// "https://erp.quan.vn". Không kèm đường dẫn.
	ServerURL string `json:"server_url"`

	// Token — cấp lúc ghép nối, gắn với MÁY TÍNH này (không phải với một máy in).
	// Chạy `pair` thêm lần nữa cho máy in khác sẽ dùng lại đúng token này. Mất
	// token thì ghép lại, không phải cài lại.
	Token string `json:"token"`

	// Printers — mọi máy in máy này đang cầm. Một tiến trình, một dịch vụ nền,
	// một vòng long-poll phục vụ hết.
	Printers []Binding `json:"printers"`

	// DrawerPin — chân RJ11 đá két: 0 = chân 2 (đa số), 1 = chân 5.
	DrawerPin int `json:"drawer_pin"`
}

// Upsert gắn thêm (hoặc cập nhật) một máy in.
//
// Gọi khi `pair` chạy lần thứ hai trên cùng máy: ghép lại chính máy in cũ thì
// đổi thiết bị, còn máy in mới thì nối vào danh sách. Không bao giờ để hai mục
// cùng một PrinterID — trùng thì vòng lặp chính mở hai lần cùng một thiết bị.
func (c *Config) Upsert(b Binding) {
	for i := range c.Printers {
		if c.Printers[i].PrinterID == b.PrinterID {
			c.Printers[i] = b
			return
		}
	}
	c.Printers = append(c.Printers, b)
}

// Load đọc config. Lỗi ở đây phải nói rõ PHẢI LÀM GÌ, vì người đọc nó là kỹ
// thuật viên đang đứng ở quán lúc 7 giờ sáng, không phải lập trình viên.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("chưa ghép nối máy in (không thấy %s) — chạy `trcf-bridge pair` trước", path)
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

// LoadForPairing đọc cấu hình cho lệnh `pair`, nơi thiếu sót là bình thường.
//
// Khác `Load`: chưa có tệp thì trả về Config RỖNG (máy chưa ghép lần nào — đúng ca
// phổ biến nhất), còn tệp có mà hỏng thì BÁO LỖI thay vì âm thầm bắt đầu lại. Ranh
// giới đó quan trọng: dựng Config rỗng khi tệp hỏng sẽ vứt mất token và danh sách
// máy in đã ghép, mà các máy in đó vẫn còn liên kết bên ERP — chúng ngừng in trong
// im lặng. Cố ý KHÔNG gọi `validate()`: cấu hình chưa đủ máy in là chuyện đương
// nhiên ở đây, vì `pair` sinh ra để bù vào chỗ thiếu đó.
func LoadForPairing(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("đọc %s: %w", path, err)
	}

	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf(
			"tệp cấu hình %s hỏng định dạng: %w\n"+
				"Ghép tiếp sẽ mất token và các máy in đã ghép. Sửa lại tệp, hoặc xoá hẳn nó "+
				"rồi ghép lại TỪNG máy in", path, err)
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
	if len(c.Printers) == 0 {
		return fmt.Errorf("chưa ghép máy in nào — chạy `pair` với mã lấy ở ERP")
	}
	seen := map[string]int{}
	for i, b := range c.Printers {
		if b.PrinterID == 0 {
			return fmt.Errorf("máy in thứ %d thiếu printer_id", i+1)
		}
		if b.Printer.Kind == "" || b.Printer.Target == "" {
			return fmt.Errorf("máy in thứ %d chưa chọn thiết bị (kind/target rỗng)", i+1)
		}
		// Hai máy in bên ERP trỏ vào CÙNG một thiết bị: xảy ra khi ghép máy in thứ hai
		// mà quên `--device`, agent tự dò rồi chọn lại đúng máy in đầu. Hậu quả là
		// phiếu bếp chui ra máy in bill, hoặc hai lệnh ghi xen kẽ cho ra một tờ ký tự
		// rác — mà chẳng có gì báo lỗi.
		key := b.Printer.Kind + "|" + b.Printer.Target
		if prev, dup := seen[key]; dup {
			return fmt.Errorf(
				"máy in id %d và id %d cùng trỏ vào %q — mỗi máy in phải một thiết bị riêng; "+
					"ghép lại bằng `pair --device <thiết bị đúng>` (xem `list`)",
				c.Printers[prev].PrinterID, b.PrinterID, b.Printer.Target)
		}
		seen[key] = i
	}
	return nil
}

// CheckWritable kiểm xem có ghi được cấu hình vào `path` hay không.
//
// Gọi TRƯỚC khi làm bất cứ việc gì đổi trạng thái bên ERP: ghép nối đổi được chủ của
// một máy in, nên "gọi ERP xong mới phát hiện thiếu quyền ghi" để lại một máy in
// thuộc về máy quầy không có token — nó ngừng in mà không báo gì.
//
// Kiểm bằng cách ghi thật một tệp tạm rồi xoá: `os.Access` không có trong Go, mà đoán
// theo quyền của thư mục thì sai ở đúng những chỗ hay gặp (thư mục chỉ đọc, hết đĩa).
func CheckWritable(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("không tạo được thư mục cấu hình %s — chạy lại với `sudo`: %w", dir, err)
	}
	probe := path + ".probe"
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		return fmt.Errorf(
			"không ghi được cấu hình vào %s — chạy lại lệnh này với `sudo`: %w", path, err)
	}
	return os.Remove(probe)
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
