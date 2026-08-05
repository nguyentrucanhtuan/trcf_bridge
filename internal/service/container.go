package service

import (
	"os"
	"strings"
)

// InContainer cho biết tiến trình đang chạy trong container hay không.
//
// Vì sao cần: `pair` mặc định cài dịch vụ systemd. Trong container thì không có systemd,
// nên không có cờ này người lắp sẽ thấy dòng "⚠ Chưa cài được dịch vụ nền" ngay sau khi
// ghép THÀNH CÔNG — đọc như hỏng, trong khi thật ra mọi thứ đều đúng và `restart:
// unless-stopped` của Docker đã lo phần tự chạy lại. Một cảnh báo sai chỗ đủ để người ta
// gỡ ra cài lại từ đầu.
//
// Hai dấu hiệu vì mỗi thứ bắt được một loại: `/.dockerenv` là Docker đặt vào, còn
// `/proc/1/cgroup` bắt thêm podman/containerd. Không bắt được thì cùng lắm là quay về
// hành vi cũ (thử cài rồi báo không cài được) — sai lệch nhẹ, không mất gì.
func InContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd") ||
		strings.Contains(s, "libpod")
}
