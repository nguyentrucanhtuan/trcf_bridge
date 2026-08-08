package printer

import (
	"errors"
	"strings"
	"testing"
)

// Hai chiếc máy in THẬT ở quán Gác Dero, đo 2026-08-07. Dùng số liệu thật thay
// vì bịa: chính cặp máy này đẻ ra sự cố in ra chữ lệnh.
var (
	billGacDero = Identity{
		Serial: "1C79EC420000", CMD: "EPSON", IDs: "0483:5743",
		USBPath: "1-3", Model: "POS-80", Mfg: "Printer",
	}
	temGacDero = Identity{
		Serial: "00270A9D5193", CMD: "TSPL2", IDs: "1fc9:2016",
		USBPath: "1-4", Model: "3B-300B", Mfg: "4BARCODE",
	}
)

func dev(path string, id Identity) Found { return Found{Path: path, Identity: id} }

// Ca 1 — khớp đúng một, và ĐÚNG CÁI ĐANG ĐỔI CHỖ.
//
// Đây là toàn bộ lý do đợt sửa tồn tại: cấu hình ghi "bill ở lp2", sau khởi động
// hai máy về lp0/lp1. Phải tìm ra máy bill ở chỗ mới, không được ghi vào lp1
// (máy tem) chỉ vì nó "gần đúng".
func TestResolveTimDungMayKhiDuongDanDoiCho(t *testing.T) {
	devices := []Found{
		dev("/dev/usb/lp0", billGacDero),
		dev("/dev/usb/lp1", temGacDero),
	}

	got, err := Resolve(billGacDero, devices)
	if err != nil {
		t.Fatalf("muốn khớp, nhận lỗi: %v", err)
	}
	if got.Device.Path != "/dev/usb/lp0" {
		t.Errorf("khớp nhầm máy: %s", got.Device.Path)
	}
	if got.Warning != "" {
		t.Errorf("không nên cảnh báo gì: %q", got.Warning)
	}
}

// Ca 2 — không khớp cái nào thì PHẢI hỏng, tuyệt đối không vơ máy còn lại.
//
// Đây là hành vi thay cho việc phun chữ lệnh ra giấy. Câu lỗi phải gọi tên máy
// để người ở quán biết rút cái nào ra cắm lại.
func TestResolveKhongKhopThiFailClosed(t *testing.T) {
	devices := []Found{dev("/dev/usb/lp1", temGacDero)} // chỉ còn máy tem

	_, err := Resolve(billGacDero, devices)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("muốn ErrNoMatch, nhận %v", err)
	}
	for _, want := range []string{"POS-80", "dây USB"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("câu lỗi %q thiếu %q", err, want)
		}
	}
}

// Ca 3 — hai máy cùng khớp thì TỪ CHỐI, không đoán.
//
// Xảy ra khi quán có hai chiếc cùng model mà firmware nhét cùng một serial —
// rủi ro đã biết với máy in nhiệt dùng chung chip Trung Quốc.
func TestResolveNhieuMayKhopThiTuChoi(t *testing.T) {
	devices := []Found{
		dev("/dev/usb/lp0", billGacDero),
		dev("/dev/usb/lp5", billGacDero),
	}

	_, err := Resolve(billGacDero, devices)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("muốn ErrAmbiguous, nhận %v", err)
	}
	// Phải liệt kê ra chỗ của chúng, nếu không người sửa mù thông tin.
	for _, want := range []string{"/dev/usb/lp0", "/dev/usb/lp5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("câu lỗi %q thiếu %q", err, want)
		}
	}
}

// Ca 4 — máy KHÔNG khai serial vẫn phải dùng được, qua khoá lùi.
//
// Bắt buộc phải có: CUPS có hẳn lỗi "hai máy in không serial làm hỏng backend",
// và có dòng máy không nhét serial vào chuỗi tự khai. Chặn cứng ca này thì máy
// in đó không gán được vai trò nào.
func TestResolveKhoaLuiKhiThieuSerial(t *testing.T) {
	khongSerial := Identity{CMD: "ESC/POS", IDs: "0483:5743", USBPath: "1-3", Model: "POS-80"}
	devices := []Found{
		dev("/dev/usb/lp0", khongSerial),
		// Cùng mã thiết bị nhưng CẮM CỔNG KHÁC — không được khớp.
		dev("/dev/usb/lp1", Identity{CMD: "ESC/POS", IDs: "0483:5743", USBPath: "1-9", Model: "POS-80"}),
	}

	got, err := Resolve(khongSerial, devices)
	if err != nil {
		t.Fatalf("muốn khớp qua khoá lùi, nhận lỗi: %v", err)
	}
	if got.Device.Path != "/dev/usb/lp0" {
		t.Errorf("khớp nhầm cổng: %s", got.Device.Path)
	}
	if got.Warning == "" {
		t.Error("khớp bằng khoá lùi thì phải nói ra — im lặng là giấu mất chỗ yếu")
	}
}

// Ca 5 — serial khớp mà mã thiết bị lệch: vẫn nhận, nhưng phải cảnh báo.
func TestResolveSerialKhopMaIDsLechThiCanhBao(t *testing.T) {
	doiBoMach := billGacDero
	doiBoMach.IDs = "1a86:7523"

	got, err := Resolve(billGacDero, []Found{dev("/dev/usb/lp0", doiBoMach)})
	if err != nil {
		t.Fatalf("serial khớp thì phải nhận: %v", err)
	}
	if !strings.Contains(got.Warning, "mã thiết bị lệch") {
		t.Errorf("thiếu cảnh báo lệch mã: %q", got.Warning)
	}
}

// Ca 14 — firmware nhét NUL và chữ hoa/thường lung tung vẫn phải khớp.
//
// Không phải phòng xa: bài học 048 (máy chấm công) — chuỗi về kèm NUL làm tên
// máy hiện ra thành một dấu cách.
func TestResolveChiuDuocNULvaHoaThuong(t *testing.T) {
	ban := billGacDero
	ban.Serial = "  1c79ec420000\x00 "

	if _, err := Resolve(billGacDero, []Found{dev("/dev/usb/lp0", ban)}); err != nil {
		t.Fatalf("NUL/hoa-thường không được làm trượt khớp: %v", err)
	}
}

// Danh tính rỗng (cấu hình đời cũ) không được khớp bừa vào máy đầu tiên.
func TestResolveDanhTinhRongThiKhongDoan(t *testing.T) {
	_, err := Resolve(Identity{}, []Found{dev("/dev/usb/lp0", billGacDero)})
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("muốn ErrNoMatch, nhận %v", err)
	}
}

// Bí danh CMD phải quy về họ. So chuỗi thô sẽ trượt CẢ HAI máy ở Gác Dero:
// máy bill khai "EPSON" chứ không phải "ESC/POS", máy tem khai "TSPL2".
func TestCommandFamilyQuyBiDanhVeHo(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"EPSON", FamilyESCPOS},
		{"ESC/POS", FamilyESCPOS},
		{"esc-pos", FamilyESCPOS},
		{"STAR", FamilyESCPOS},
		{"TSPL2", FamilyTSPL},
		{"TSPL", FamilyTSPL},
		{"ZPL", FamilyTSPL},
		{"EPL2", FamilyTSPL},
		{"ESCPOS,ESC/POS", FamilyESCPOS}, // danh sách: lấy token nhận ra được
		{"PCL5", ""},                     // không nhận diện được ≠ không hợp lệ
		{"", ""},
	}
	for _, tc := range tests {
		if got := CommandFamily(tc.cmd); got != tc.want {
			t.Errorf("CommandFamily(%q) = %q, muốn %q", tc.cmd, got, tc.want)
		}
	}
}

// Thiếu CMD một bên KHÔNG được chặn khớp: "không biết" khác "khác nhau".
func TestKhoaLuiKhongChanKhiThieuCMD(t *testing.T) {
	want := Identity{IDs: "0483:5743", USBPath: "1-3"}
	got := Identity{IDs: "0483:5743", USBPath: "1-3", CMD: "EPSON"}
	if ok, _ := matchIdentity(want, got); !ok {
		t.Error("thiếu CMD bên cấu hình mà chặn khớp là sai")
	}
}

// Khác họ lệnh thì khoá lùi phải trượt — máy tem không được nhận vai trò máy bill.
func TestKhoaLuiTruotKhiKhacHoLenh(t *testing.T) {
	want := Identity{IDs: "0483:5743", USBPath: "1-3", CMD: "EPSON"}
	got := Identity{IDs: "0483:5743", USBPath: "1-3", CMD: "TSPL2"}
	if ok, _ := matchIdentity(want, got); ok {
		t.Error("ESC/POS và TSPL2 không được coi là cùng một máy")
	}
}

// MatchKey gom hàng đợi theo CHIẾC MÁY. Cùng serial = cùng khoá, dù đường dẫn
// đã đổi — đây là điều kiện để hai vai trò dùng chung máy không đá nhau.
func TestMatchKeyTheoChiecMayKhongTheoDuongDan(t *testing.T) {
	if billGacDero.MatchKey() != billGacDero.MatchKey() {
		t.Fatal("khoá phải ổn định")
	}
	if billGacDero.MatchKey() == temGacDero.MatchKey() {
		t.Error("hai chiếc khác nhau không được chung khoá")
	}
	khongSerial := Identity{IDs: "0483:5743", USBPath: "1-3"}
	if !strings.HasPrefix(khongSerial.MatchKey(), "hw:") {
		t.Errorf("thiếu serial thì khoá phải rơi về phần cứng: %s", khongSerial.MatchKey())
	}
}

// device_key phải đi được một vòng ERP rồi về mà không mất trường khớp nào.
func TestKeyRoundTrip(t *testing.T) {
	for _, want := range []Identity{billGacDero, temGacDero, {IDs: "0483:5743", USBPath: "1-3"}} {
		got, err := ParseIdentity(want.Key())
		if err != nil {
			t.Fatalf("ParseIdentity(%q): %v", want.Key(), err)
		}
		if got.Serial != want.Serial || got.CMD != want.CMD || got.IDs != want.IDs || got.USBPath != want.USBPath {
			t.Errorf("mất trường khớp sau vòng lặp: %+v ≠ %+v", got, want)
		}
		if got.MatchKey() != want.MatchKey() {
			t.Errorf("khoá gom nhóm đổi sau vòng lặp: %s ≠ %s", got.MatchKey(), want.MatchKey())
		}
	}
}

func TestParseIdentityTuChoiChuoiLa(t *testing.T) {
	if _, err := ParseIdentity("net://192.168.1.60:9100"); err == nil {
		t.Error("chuỗi không phải usb:// thì phải báo lỗi")
	}
}
