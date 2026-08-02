//go:build linux

// Package service cài agent thành dịch vụ chạy nền.
//
// Bắt buộc phải tự chạy lại sau khi khởi động máy: máy quầy bị tắt mỗi tối và
// nhân viên mở máy sáng hôm sau sẽ không ai nhớ bấm chạy agent.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const unitPath = "/etc/systemd/system/trcf-print-agent.service"

// unitTemplate — %s là đường dẫn tuyệt đối tới binary.
//
// Vì sao chạy dưới root: agent ghi vào /dev/usb/lp0 (mặc định root:lp 0660).
// Có thể chạy dưới user thường + thêm vào nhóm `lp` + udev rule, nhưng ở quán
// thì một dịch vụ đơn giản chạy được ngay quan trọng hơn — và agent không mở
// cổng nghe, không nhận dữ liệu từ bên ngoài, nên bề mặt tấn công rất hẹp.
const unitTemplate = `[Unit]
Description=TRCF Print Agent — in bill/phiếu bếp cho TRCF ERP
Documentation=https://github.com/nguyentrucanhtuan/trcf_print_agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run
Restart=always
RestartSec=5
# Máy in tắt / rớt mạng là chuyện thường ngày ở quán — dịch vụ phải bò dậy mãi,
# không được để systemd bỏ cuộc sau vài lần thất bại.
StartLimitIntervalSec=0
User=root
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`

// Install ghi unit, nạp lại systemd, bật tự khởi động và chạy ngay.
func Install() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("không xác định được đường dẫn agent: %w", err)
	}

	if os.Geteuid() != 0 {
		return fmt.Errorf("cần quyền root để cài dịch vụ — chạy lại với `sudo`")
	}

	unit := fmt.Sprintf(unitTemplate, exe)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("ghi %s: %w", unitPath, err)
	}

	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", "trcf-print-agent"},
		{"restart", "trcf-print-agent"},
	} {
		cmd := exec.Command("systemctl", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %w: %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Installed cho biết unit đã tồn tại chưa (để `pair` không cài đè vô ích).
func Installed() bool {
	_, err := os.Stat(unitPath)
	return err == nil
}
