package printer

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Identity — DANH TÍNH của một máy in, thứ thay thế cho đường dẫn làm địa chỉ bền vững.
//
// ─────────────────────────────────────────────────────────────────────────
// VÌ SAO TỒN TẠI — sự cố quán Gác Dero, 2026-08-08:
//
// Nhân Linux cấp `/dev/usb/lpN` theo THỨ TỰ TÌM THẤY, và thứ tự đó đổi sau mỗi
// lần khởi động. Quán có hai máy in: lúc cài chúng nằm ở lp1/lp2, sau một lần
// khởi động sạch chúng về lp0/lp1. Cấu hình ghi cứng "tem→lp1, bill→lp2" nên
// lệnh in TEM (TSPL2) chảy vào MÁY BILL (ESC/POS), và máy bill in ra nguyên văn
// chữ lệnh: `SIZE 50 mm,30 mm`, `GAP`, `TEXT`, `PRINT 1`.
//
// Agent ghi mù vào một đường dẫn mà không bao giờ hỏi "đầu bên kia có phải máy
// của tôi không". Identity là câu hỏi đó.
// ─────────────────────────────────────────────────────────────────────────
//
// Trường nào dùng để KHỚP, trường nào chỉ để NGƯỜI ĐỌC — phân biệt này là quan
// trọng nhất của cả tệp:
//
//   - Serial            → khớp *chiếc máy nào*
//   - CMD               → khớp *loại máy nào* (ESC/POS hay TSPL)
//   - IDs + USBPath     → khoá lùi khi máy không khai serial
//   - Model, Mfg        → CHỈ hiển thị, KHÔNG bao giờ tham gia khớp
//
// Model không khớp được vì hai máy cùng model cho ra hai dòng chữ y hệt nhau.
// Tệ hơn, tên USB có thể sai lệch hẳn: tại Gác Dero máy in TEM 4BARCODE tự khai
// tên sản phẩm USB là "Printer-80" (nghe như máy bill 80mm), còn máy bill thật
// mang tên vô nghĩa "USB Printer Port". Chỉ `ieee1284_id` mới đáng tin.
type Identity struct {
	Serial  string `json:"serial,omitempty"`
	CMD     string `json:"cmd,omitempty"`
	IDs     string `json:"ids,omitempty"`
	USBPath string `json:"usb_path,omitempty"`
	Model   string `json:"model,omitempty"`
	Mfg     string `json:"mfg,omitempty"`
}

// Họ ngôn ngữ lệnh. Máy in nói hai thứ tiếng KHÔNG thay thế được cho nhau:
// gửi ESC/POS sang máy tem thì nhả giấy trắng, gửi TSPL sang máy bill thì in ra
// chữ lệnh thành rác.
const (
	FamilyESCPOS = "escpos"
	FamilyTSPL   = "tspl"
)

// cmdFamilies — bí danh do hãng đặt, quy về họ.
//
// PHẢI chuẩn hoá chứ không so chuỗi thô: tại Gác Dero máy bill khai `CMD:EPSON`
// (không phải chữ "ESC/POS") và máy tem khai `CMD:TSPL2` (không phải "TSPL").
// So thô là trượt CẢ HAI máy — tức là luật "vai trò phải khớp loại máy" sẽ chặn
// nhầm đúng hai chiếc đang chạy tốt.
//
// Danh sách này mở rộng dần khi gặp máy mới; thêm một dòng, không đổi thiết kế.
var cmdFamilies = map[string]string{
	"EPSON":    FamilyESCPOS,
	"ESC/POS":  FamilyESCPOS,
	"ESCPOS":   FamilyESCPOS,
	"ESC-POS":  FamilyESCPOS,
	"STAR":     FamilyESCPOS,
	"STARPRNT": FamilyESCPOS,
	"TSPL":     FamilyTSPL,
	"TSPL2":    FamilyTSPL,
	"EPL":      FamilyTSPL,
	"EPL2":     FamilyTSPL,
	"ZPL":      FamilyTSPL,
	"ZPL2":     FamilyTSPL,
}

// CommandFamily quy chuỗi CMD về họ, trả "" khi KHÔNG NHẬN DIỆN ĐƯỢC.
//
// Chuỗi rỗng có nghĩa "không biết", KHÔNG có nghĩa "không hợp lệ" — và người gọi
// phải xử lý hai thứ đó khác nhau. Chặn ca "không biết" sẽ làm một máy in không
// khai CMD không gán được vai trò nào, tức là quán không in được gì: hỏng nặng
// hơn chính lỗi đang sửa.
//
// CMD có thể là danh sách ("ESCPOS,ESC/POS") — lấy token đầu tiên nhận ra được.
func CommandFamily(cmd string) string {
	for _, tok := range strings.Split(cmd, ",") {
		if fam, ok := cmdFamilies[cleanID(tok)]; ok {
			return fam
		}
	}
	return ""
}

// cleanID chuẩn hoá một mẩu chuỗi máy in tự khai trước khi so sánh.
//
// Cắt ký tự NUL là bắt buộc, không phải phòng xa: firmware máy in hay nhét NUL
// vào chuỗi tự khai, và đã có tiền lệ ở dự án này (bài học 048 — tên máy chấm
// công về kèm NUL, hiện ra thành một dấu cách).
func cleanID(s string) string {
	return strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(s, "\x00", "")))
}

// IsZero — danh tính rỗng, tức là cấu hình đời cũ chỉ có đường dẫn.
func (i Identity) IsZero() bool {
	return i.Serial == "" && i.CMD == "" && i.IDs == "" && i.USBPath == "" && i.Model == ""
}

// MatchKey — khoá gom nhóm cho MỘT CHIẾC MÁY vật lý.
//
// Dùng để gom hàng đợi in: nhiều máy in bên ERP (bill + phiếu bếp) có thể cùng
// trỏ vào một chiếc máy, và chúng PHẢI dùng chung một hàng đợi vì `usblp` chỉ
// cho một tiến trình mở thiết bị tại một thời điểm.
//
// Khoá theo DANH TÍNH chứ không theo đường dẫn — đây là chi tiết dễ sai nhất:
// khoá theo `/dev/usb/lp0` thì lúc đường dẫn đổi giữa phiên chạy, cùng một máy
// sẽ đẻ ra hai thợ, và ta quay lại đúng lỗi đang sửa.
func (i Identity) MatchKey() string {
	if s := cleanID(i.Serial); s != "" {
		return "sn:" + s
	}
	return "hw:" + cleanID(i.IDs) + "@" + cleanID(i.USBPath)
}

// Label — dòng chữ cho người lắp đặt và cho màn Máy in bên ERP.
func (i Identity) Label() string {
	parts := make([]string, 0, 3)
	switch {
	case i.Mfg != "" && i.Model != "":
		parts = append(parts, i.Mfg+" "+i.Model)
	case i.Model != "":
		parts = append(parts, i.Model)
	case i.Mfg != "":
		parts = append(parts, i.Mfg)
	}
	if i.CMD != "" {
		parts = append(parts, i.CMD)
	}
	if i.Serial != "" {
		parts = append(parts, "SN "+i.Serial)
	}
	if len(parts) == 0 {
		return "máy in không khai danh tính"
	}
	return strings.Join(parts, " · ")
}

// Key — dạng chuỗi để ERP lưu vào `print_agent_bindings.device_key`.
//
// Có cấu trúc cho người đọc, nhưng backend CỐ Ý không phân tích: nó chỉ lưu và
// so chuỗi. Nhờ vậy bản Windows sau này nhét `win://<tên spooler>` vào đúng cột
// đó mà không phải sửa một dòng backend nào.
//
//	usb://POS-80?cmd=EPSON&ids=0483:5743&serial=1C79EC420000
//
// Model đi ở phần host là để đọc cho dễ; nó KHÔNG tham gia khớp nên việc
// escape/unescape có sai lệch cũng vô hại.
func (i Identity) Key() string {
	q := url.Values{}
	add := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	add("serial", i.Serial)
	add("cmd", i.CMD)
	add("ids", i.IDs)
	add("usb_path", i.USBPath)
	add("mfg", i.Mfg)

	host := i.Model
	if host == "" {
		host = "printer"
	}
	return "usb://" + url.PathEscape(host) + "?" + q.Encode()
}

// ParseIdentity đọc ngược `Key()`. Chuỗi không phải dạng `usb://` → lỗi.
func ParseIdentity(s string) (Identity, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Identity{}, fmt.Errorf("danh tính máy in không đọc được %q: %w", s, err)
	}
	if u.Scheme != "usb" {
		return Identity{}, fmt.Errorf("danh tính máy in %q không phải dạng usb://", s)
	}
	q := u.Query()
	model, err := url.PathUnescape(u.Host)
	if err != nil || model == "printer" {
		model = ""
	}
	return Identity{
		Serial:  q.Get("serial"),
		CMD:     q.Get("cmd"),
		IDs:     q.Get("ids"),
		USBPath: q.Get("usb_path"),
		Mfg:     q.Get("mfg"),
		Model:   model,
	}, nil
}

// Lỗi phân giải. Người gọi phân biệt được hai ca vì cách xử KHÁC HẲN nhau:
// không thấy máy thì báo hỏng lệnh in, còn thấy nhiều máy thì bắt người chọn lại.
var (
	ErrNoMatch   = errors.New("không thấy máy in khớp danh tính")
	ErrAmbiguous = errors.New("nhiều máy in cùng khớp danh tính")
)

// Resolution — kết quả phân giải.
type Resolution struct {
	Device  Found
	Warning string // rỗng nếu không có gì đáng ngờ
}

// Resolve tìm thiết bị đang cắm ứng với một danh tính đã ghi.
//
// HÀM THUẦN, cố ý: nó nhận sẵn danh sách thiết bị thay vì tự đi dò, nên toàn bộ
// ma trận quyết định test được trên máy không có máy in — cùng lý do
// `writeall.go` được tách khỏi `usb_linux.go`.
//
// Ba nhánh, và nhánh nào cũng phải rõ ràng:
//
//	1 khớp  → trả về, dù hôm nay nó là lp0 hay lp7
//	0 khớp  → lỗi có chữ (fail-closed) — TUYỆT ĐỐI không ghi bừa vào máy còn lại
//	≥2 khớp → từ chối, đòi chọn lại; đoán bừa là in nhầm máy
func Resolve(want Identity, devices []Found) (Resolution, error) {
	if want.IsZero() {
		return Resolution{}, fmt.Errorf("%w: chưa ghi danh tính máy in", ErrNoMatch)
	}

	var hits []Resolution
	for _, d := range devices {
		if ok, warn := matchIdentity(want, d.Identity); ok {
			hits = append(hits, Resolution{Device: d, Warning: warn})
		}
	}

	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Resolution{}, fmt.Errorf(
			"%w: %s — kiểm tra dây USB và nguồn máy in, hoặc vào ERP mục Máy in chọn lại thiết bị",
			ErrNoMatch, want.Label())
	default:
		paths := make([]string, 0, len(hits))
		for _, h := range hits {
			paths = append(paths, h.Device.Path)
		}
		return Resolution{}, fmt.Errorf(
			"%w: %d máy in cùng khớp %s (%s) — vào ERP mục Máy in chọn lại đúng thiết bị",
			ErrAmbiguous, len(hits), want.Label(), strings.Join(paths, ", "))
	}
}

// matchIdentity — luật khớp, theo đúng thứ tự ưu tiên.
//
// ① Hai bên đều có serial  → khớp serial. Serial là thứ duy nhất phân biệt được
// hai chiếc CÙNG model, nên khi có nó thì nó thắng.
//
// ② Thiếu serial một bên  → khoá lùi: mã thiết bị + vị trí cổng USB + cùng họ
// lệnh. Bắt buộc phải có nhánh này, KHÔNG được giả định máy in nào cũng khai
// serial: CUPS có hẳn lỗi "hai máy in không serial làm hỏng backend", và có
// dòng máy không nhét serial vào chuỗi tự khai.
func matchIdentity(want, got Identity) (bool, string) {
	wSN, gSN := cleanID(want.Serial), cleanID(got.Serial)
	if wSN != "" && gSN != "" {
		if wSN != gSN {
			return false, ""
		}
		// Serial khớp mà mã thiết bị lệch: hàng thay bo mạch, hoặc serial trùng
		// giữa hai chiếc khác model. Vẫn nhận (serial là khoá mạnh nhất) nhưng
		// phải nói ra — im lặng ở đây là bịt mất manh mối cho lần sau.
		if wIDs, gIDs := cleanID(want.IDs), cleanID(got.IDs); wIDs != "" && gIDs != "" && wIDs != gIDs {
			return true, fmt.Sprintf("serial khớp nhưng mã thiết bị lệch (%s ≠ %s)", want.IDs, got.IDs)
		}
		return true, ""
	}

	wIDs, gIDs := cleanID(want.IDs), cleanID(got.IDs)
	wPath, gPath := cleanID(want.USBPath), cleanID(got.USBPath)
	if wIDs == "" || gIDs == "" || wIDs != gIDs {
		return false, ""
	}
	if wPath == "" || gPath == "" || wPath != gPath {
		return false, ""
	}
	if !commandsCompatible(want.CMD, got.CMD) {
		return false, ""
	}
	return true, "máy in không khai serial — đang khớp theo mã thiết bị và vị trí cổng USB"
}

// commandsCompatible — hai chuỗi CMD có cùng họ không.
//
// Thiếu CMD một bên thì KHÔNG chặn: "không biết" khác "khác nhau". Ở nhánh khoá
// lùi thì mã thiết bị và vị trí cổng đã đủ hẹp rồi.
func commandsCompatible(want, got string) bool {
	if strings.TrimSpace(want) == "" || strings.TrimSpace(got) == "" {
		return true
	}
	wf, gf := CommandFamily(want), CommandFamily(got)
	if wf != "" && gf != "" {
		return wf == gf
	}
	return cleanID(want) == cleanID(got)
}
