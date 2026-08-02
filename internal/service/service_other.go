//go:build !linux

package service

import (
	"fmt"
	"runtime"
)

// Bản này chỉ để build được trên máy dev không phải Linux. Cài dịch vụ nền cho
// Windows (Windows Service) là đợt sau — PRD FR-047.10.

func Install() error {
	return fmt.Errorf("cài dịch vụ nền chưa hỗ trợ trên %s (hiện chỉ Linux)", runtime.GOOS)
}

func Installed() bool { return false }
