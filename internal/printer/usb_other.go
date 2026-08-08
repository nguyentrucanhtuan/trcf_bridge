//go:build !linux

package printer

import "runtime"

// Bản này chỉ để agent BUILD ĐƯỢC trên máy dev không phải Linux (macOS/Windows).
// Đường USB thật hiện chỉ có cho Linux — Windows (winspool RAW) là đợt sau,
// xem PRD FR-047.11.
//
// Đường KindNet vẫn chạy đầy đủ trên mọi hệ điều hành, nên `test --net` dùng
// được ngay trên máy dev để thử máy in LAN.

func openUSB(target string, id *Identity) (Printer, error) {
	return nil, &unsupportedError{op: "kết nối máy in USB"}
}

type unsupportedError struct{ op string }

func (e *unsupportedError) Error() string {
	return e.op + " chưa hỗ trợ trên " + runtime.GOOS + " (hiện chỉ Linux; Windows làm ở đợt sau)"
}
