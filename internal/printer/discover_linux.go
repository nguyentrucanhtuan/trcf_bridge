//go:build linux

package printer

// openUSB KHÔNG kiểm tra device có tồn tại hay không — cố ý.
//
// Agent phải khởi động được cả khi máy in đang tắt hoặc chưa cắm, rồi báo lỗi
// THEO TỪNG LẦN IN để ERP hiện "máy in mất kết nối" (PRD FR-047.9/.14). Nếu
// chặn ngay lúc Open thì máy in tắt buổi tối = agent chết, sáng bật máy in lên
// vẫn không in được vì agent đã thoát từ đêm qua.
func openUSB(target string, id *Identity) (Printer, error) {
	return &usbPrinter{path: target, identity: id}, nil
}
