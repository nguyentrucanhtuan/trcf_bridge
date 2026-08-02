// Package escpos chứa ĐÚNG lượng ESC/POS tối thiểu để agent tự kiểm tra được
// máy in lúc lắp đặt.
//
// ⚠️ Đây KHÔNG phải nơi dựng bill. Bill và phiếu bếp do BACKEND dựng và gửi
// xuống dạng byte hoàn chỉnh (PRD FR-047.1) — nguyên tắc "agent phải ngu": đổi
// mẫu bill thì deploy backend, không phải đi cập nhật agent ở từng quán.
//
// Gói này chỉ phục vụ `list`/`test` — thứ người lắp đặt cần để biết máy in sống
// và đá được két, TRƯỚC khi có backend.
package escpos

import (
	"bytes"
	"fmt"
	"time"
)

// Lệnh ESC/POS dùng trong trang thử.
var (
	// ESC @ — khởi tạo, xoá mọi định dạng còn sót của lần in trước.
	cmdInit = []byte{0x1B, 0x40}
	// ESC a n — canh lề: 0 trái, 1 giữa.
	cmdAlignCenter = []byte{0x1B, 0x61, 0x01}
	cmdAlignLeft   = []byte{0x1B, 0x61, 0x00}
	// GS ! n — cỡ chữ: 0x11 = cao đôi + rộng đôi.
	cmdSizeDouble = []byte{0x1D, 0x21, 0x11}
	cmdSizeNormal = []byte{0x1D, 0x21, 0x00}
	// ESC E n — in đậm.
	cmdBoldOn  = []byte{0x1B, 0x45, 0x01}
	cmdBoldOff = []byte{0x1B, 0x45, 0x00}
	// ESC d n — đẩy giấy n dòng (chừa chỗ cho dao cắt).
	cmdFeed4 = []byte{0x1B, 0x64, 0x04}
	// GS V m — cắt giấy. m=66 (partial cut sau khi đẩy giấy) là lệnh được nhiều
	// máy Trung Quốc hỗ trợ nhất; m=0/1 (full/partial cut ngay) thì một số máy
	// cắt trúng chữ vì chưa đẩy hết giấy qua dao.
	cmdCut = []byte{0x1D, 0x56, 0x42, 0x00}
)

// Cut — lệnh cắt giấy đứng riêng, để `test` gọi được mà không cần in gì.
func Cut() []byte { return append([]byte{}, cmdCut...) }

// KickDrawer sinh lệnh đá ngăn kéo tiền: ESC p m t1 t2.
//
// pin: chân trên cổng RJ11 của MÁY IN (két cắm vào máy in, không cắm vào máy
// tính) — 0 là chân 2, 1 là chân 5. Đa số két bán ở VN dùng chân 2; gặp két
// không bật thì thử chân 5 trước khi nghi hỏng.
//
// t1/t2 là thời gian giữ điện (đơn vị 2ms). 25×2ms = 50ms bật, 250×2ms = 500ms
// nghỉ — mức phổ biến; ngắn quá thì solenoid không đủ lực đẩy chốt.
func KickDrawer(pin int) []byte {
	m := byte(0x00)
	if pin == 1 {
		m = 0x01
	}
	return []byte{0x1B, 0x70, m, 0x19, 0xFA}
}

// TestPage dựng trang in thử cho người lắp đặt.
//
// Nó cố tình trả lời BỐN câu hỏi trong một tờ giấy, vì người lắp đang đứng
// cạnh máy in và chỉ có một cơ hội nhìn:
//  1. Máy in có nhận byte không (có chữ ra không)
//  2. Cắt giấy có chạy không (tờ giấy có đứt rời không)
//  3. Chữ tiếng Việt có dấu ra đúng hay thành ký tự rác  ← xem ghi chú dưới
//  4. Bề rộng giấy là 58mm hay 80mm (thước đếm ký tự)
//
// (3) là câu quan trọng mà dễ bị bỏ sót: máy in nhiệt mặc định dùng bảng mã
// không có tiếng Việt. Nếu dòng có dấu ra rác thì backend sẽ phải in chữ dưới
// dạng ẢNH thay vì text — đó là thay đổi lớn ở FR-047.2, nên phải biết SỚM,
// ngay lúc lắp máy, chứ không phải lúc quán đã chạy.
func TestPage(agentVersion, target string) []byte {
	var b bytes.Buffer

	b.Write(cmdInit)

	b.Write(cmdAlignCenter)
	b.Write(cmdSizeDouble)
	b.WriteString("TRCF ERP\n")
	b.Write(cmdSizeNormal)
	b.Write(cmdBoldOn)
	b.WriteString("IN THU / TEST PAGE\n")
	b.Write(cmdBoldOff)
	b.WriteString("--------------------------------\n")

	b.Write(cmdAlignLeft)
	fmt.Fprintf(&b, "Thoi gian: %s\n", time.Now().Format("02/01/2006 15:04:05"))
	fmt.Fprintf(&b, "May in   : %s\n", target)
	fmt.Fprintf(&b, "Agent    : %s\n", agentVersion)
	b.WriteString("--------------------------------\n")

	// Thước đo bề rộng: 32 ký tự vừa khít khổ 58mm; khổ 80mm in được 48 ký tự
	// nên hàng "48" sẽ không bị xuống dòng. Nhìn tờ giấy là biết khổ nào.
	b.WriteString("Do rong giay (font A):\n")
	b.WriteString("12345678901234567890123456789012 <-32\n")
	b.WriteString("123456789012345678901234567890123456789012345678 <-48\n")
	b.WriteString("--------------------------------\n")

	// Câu (3): dòng có dấu. Ra rác = máy in không hỗ trợ bảng mã tiếng Việt.
	b.WriteString("Tieng Viet KHONG dau: OK\n")
	b.WriteString("Tiếng Việt CÓ dấu: Cà phê sữa đá\n")
	b.WriteString("(dong tren ra ky tu la => bao ky thuat)\n")

	b.Write(cmdFeed4)
	b.Write(cmdCut)
	return b.Bytes()
}
