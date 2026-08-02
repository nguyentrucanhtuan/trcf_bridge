//go:build !linux

package printer

import "runtime"

// Bản này chỉ để agent BUILD ĐƯỢC trên máy dev không phải Linux (macOS/Windows).
// Đường USB thật hiện chỉ có cho Linux — Windows (winspool RAW) là đợt sau,
// xem PRD FR-047.11.
//
// Đường KindNet vẫn chạy đầy đủ trên mọi hệ điều hành, nên `test --net` dùng
// được ngay trên máy dev để thử máy in LAN.

func openUSB(target string) (Printer, error) {
	return nil, &unsupportedError{op: "kết nối máy in USB"}
}

// Discover trả danh sách rỗng thay vì lỗi: `list` trên máy dev nên hiện "không
// thấy máy in USB" một cách bình thường, không phải nổ lỗi.
func Discover() ([]Found, error) { return nil, nil }

type unsupportedError struct{ op string }

func (e *unsupportedError) Error() string {
	return e.op + " chưa hỗ trợ trên " + runtime.GOOS + " (hiện chỉ Linux; Windows làm ở đợt sau)"
}
