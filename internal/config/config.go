// Package config đọc/ghi tệp cấu hình duy nhất của agent.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

// DeviceKey — khoá chỉ MỘT CHIẾC MÁY vật lý, dùng để gom hàng đợi in.
//
// Ưu tiên danh tính, chỉ rơi về đường dẫn khi cấu hình còn đời cũ. Khoá theo
// danh tính là bắt buộc: khoá theo `/dev/usb/lp0` thì lúc đường dẫn đổi giữa
// phiên chạy, cùng một chiếc máy sẽ đẻ ra hai hàng đợi — và hai hàng đợi cùng
// ghi vào một thiết bị chính là lỗi mà việc gom hàng đợi sinh ra để chặn.
func (b Binding) DeviceKey() string {
	if id := b.Printer.Identity; id != nil && !id.IsZero() {
		return id.MatchKey()
	}
	return "path:" + b.Printer.Target
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

// DeviceAssignment — một dòng trong bản gán ERP đang giữ (047f).
//
// Kiểu riêng thay vì dùng thẳng kiểu của gói `transport`: gói `config` không được
// phụ thuộc vào giao thức HTTP, và nhờ vậy `ApplyBindings` kiểm được bằng dữ liệu
// dựng tay, không cần máy chủ giả.
type DeviceAssignment struct {
	PrinterID int
	DeviceKey string
}

// ApplyBindings thay bản gán cục bộ bằng bản ERP đang giữ (047f, CAP-6).
//
// Thay TRỌN chứ không vá từng dòng: máy in bị gỡ trên web phải biến mất khỏi agent,
// mà một phép hợp-nhất chỉ-thêm thì không diễn đạt được việc biến mất.
//
// ⚠ MỘT NGOẠI LỆ CÓ CHỦ ĐÍCH: danh sách rỗng KHÔNG xoá sạch cấu hình đang chạy. Hai
// hỏng hóc bất đối xứng nhau — xoá nhầm thì cả quán ngừng in và không ai hiểu vì sao
// (agent vẫn xanh, vẫn hỏi việc, chỉ là không còn máy in nào); giữ thừa thì một máy in
// đã gỡ vẫn hỏi việc mà chẳng bao giờ có việc, vô hại. Nên khi ERP nói "không có gì
// cả" — dấu hiệu của lỗi phía kia hơn là ý định thật — thì giữ nguyên và kêu to.
//
// Trả về `changed` (có gì đổi không, để biết có phải ghi tệp và nạp lại hay không) và
// `warnings` (những dòng phải nói ra cho người đọc nhật ký).
func (c *Config) ApplyBindings(remote []DeviceAssignment) (changed bool, warnings []string) {
	if len(remote) == 0 {
		if len(c.Printers) > 0 {
			warnings = append(warnings,
				"ERP báo máy quầy này không cầm máy in nào — GIỮ NGUYÊN cấu hình đang chạy để quán không mất đường in. "+
					"Nếu thật sự muốn gỡ hết, gỡ từng máy in ở màn Máy in.")
		}
		return false, warnings
	}

	next := make([]Binding, 0, len(remote))
	for _, r := range remote {
		old := c.find(r.PrinterID)
		b, warn := bindingFor(r, old)
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if b == nil {
			// Không dựng nổi mục mới: giữ mục cũ nếu có, còn không thì bỏ qua. Bỏ qua
			// im lặng ở đây là được vì `warn` phía trên đã nói lý do.
			if old != nil {
				next = append(next, *old)
			}
			continue
		}
		next = append(next, *b)
	}

	if sameBindings(c.Printers, next) {
		return false, warnings
	}
	c.Printers = next
	return true, warnings
}

func (c *Config) find(printerID int) *Binding {
	for i := range c.Printers {
		if c.Printers[i].PrinterID == printerID {
			return &c.Printers[i]
		}
	}
	return nil
}

// bindingFor dựng một mục cấu hình từ một dòng bản gán.
//
// `device_key` là chuỗi MỜ với ERP nhưng agent thì phải hiểu, vì chính agent đã dựng
// ra nó. Ba dạng:
//   - `usb://…`      → máy in USB, phân giải theo danh tính (đường chính của 047f)
//   - `host:port`    → máy in LAN, danh tính vốn đã bền vững, không đụng
//   - còn lại        → không dựng được ⇒ giữ mục cũ + nói ra
func bindingFor(r DeviceAssignment, old *Binding) (*Binding, string) {
	key := strings.TrimSpace(r.DeviceKey)
	if key == "" {
		if old != nil {
			return old, ""
		}
		return nil, fmt.Sprintf("máy in id %d: ERP chưa gán thiết bị nào — chọn thiết bị ở màn Máy in", r.PrinterID)
	}

	if strings.HasPrefix(key, "usb://") {
		id, err := printer.ParseIdentity(key)
		if err != nil {
			return nil, fmt.Sprintf("máy in id %d: không đọc được danh tính ERP gửi xuống (%v)", r.PrinterID, err)
		}
		b := Binding{PrinterID: r.PrinterID, Printer: printer.Config{Kind: printer.KindUSB, Identity: &id}}
		// Giữ `target` cũ khi vẫn là USB: nó là gợi ý chỗ nằm gần nhất và là đường lùi
		// nếu ai đó hạ binary về bản trước 047f.
		if old != nil && old.Printer.Kind == printer.KindUSB {
			b.Printer.Target = old.Printer.Target
		}
		return &b, ""
	}

	if netTarget.MatchString(key) {
		return &Binding{PrinterID: r.PrinterID, Printer: printer.Config{Kind: printer.KindNet, Target: key}}, ""
	}

	if old != nil {
		return old, ""
	}
	return nil, fmt.Sprintf(
		"máy in id %d: thiết bị %q không hiểu được (cấu hình đời cũ?) — vào màn Máy in chọn lại thiết bị",
		r.PrinterID, key)
}

// `host:port` — đủ hẹp để không nuốt nhầm một đường dẫn device node đời cũ
// (`/dev/usb/lp0` có dấu `/` nên trượt).
var netTarget = regexp.MustCompile(`^[^\s/]+:\d{1,5}$`)

func sameBindings(a, b []Binding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].PrinterID != b[i].PrinterID || a[i].DeviceKey() != b[i].DeviceKey() {
			return false
		}
	}
	return true
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
	for i, b := range c.Printers {
		if b.PrinterID == 0 {
			return fmt.Errorf("máy in thứ %d thiếu printer_id", i+1)
		}
		if b.Printer.Kind == "" {
			return fmt.Errorf("máy in thứ %d chưa chọn loại kết nối (kind rỗng)", i+1)
		}
		// Từ 047f, DANH TÍNH mới là địa chỉ; `target` chỉ là chỗ nó nằm hôm nay. Một máy
		// in gán lần đầu trên web hoàn toàn có thể chưa có `target` nào — agent sẽ tự dò
		// ra lúc in. Đòi `target` ở đây thì đúng thao tác mà 047f sinh ra để phục vụ lại
		// làm agent CHẾT ở lần khởi động kế, và cả quán mất đường in vì một tệp cấu hình
		// hợp lệ.
		if b.Printer.Target == "" && b.Printer.Identity.IsZeroOrNil() {
			return fmt.Errorf("máy in thứ %d chưa chọn thiết bị (không có địa chỉ lẫn danh tính)", i+1)
		}
	}
	return nil
}

// Warnings — chuyện đáng ngờ nhưng KHÔNG chặn agent chạy.
//
// ─────────────────────────────────────────────────────────────────────────
// Trước đây "hai máy in bên ERP cùng trỏ một thiết bị" là LỖI và agent từ chối
// khởi động. Luật đó viết ra để bắt lỗi vô ý (ghép máy thứ hai mà quên
// `--device`, agent tự dò rồi chọn lại đúng máy đầu) — nhưng nó cấm luôn một ca
// dùng HỢP LỆ và phổ biến ở quán nhỏ: **một máy POS-80 in cả bill lẫn phiếu bếp**.
//
// Đó là chuẩn mực ngoài ngành, không phải chắp vá — CUPS gọi là *printer
// instances*: nhiều hàng đợi trỏ cùng một thiết bị vật lý, mỗi hàng đợi mang cấu
// hình riêng.
//
// Nguy cơ thật của việc dùng chung KHÔNG nằm ở cấu hình mà ở đồng thời: `usblp`
// chỉ cho một tiến trình mở thiết bị tại một thời điểm. Chỗ đó xử lý bằng cách
// gom hàng đợi theo THIẾT BỊ ở `cmd/agent`, không phải bằng cách cấm khai báo.
// ─────────────────────────────────────────────────────────────────────────
func (c *Config) Warnings() []string {
	var out []string
	seen := map[string]int{}
	for i, b := range c.Printers {
		key := b.Printer.Kind + "|" + b.DeviceKey()
		if prev, dup := seen[key]; dup {
			out = append(out, fmt.Sprintf(
				"máy in id %d và id %d dùng CHUNG một thiết bị (%s) — hợp lệ khi cố ý "+
					"(vd một máy in cả bill lẫn phiếu bếp); nếu không cố ý thì ghép lại "+
					"bằng `pair --device <thiết bị đúng>` (xem `list`)",
				c.Printers[prev].PrinterID, b.PrinterID, b.Printer.Target))
			continue
		}
		seen[key] = i
	}
	return out
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
