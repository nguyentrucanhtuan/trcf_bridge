package printer

import (
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	dialTimeout    = 5 * time.Second
	defaultRawPort = "9100"
)

// netPrinter — máy in LAN, giao thức "raw JetDirect": mở TCP tới cổng 9100,
// ghi byte, đóng. Không bắt tay, không phản hồi.
//
// Đường này là DỰ PHÒNG trong agent. Khi backend chạy cùng LAN với máy in thì
// backend tự mở TCP (PRD FR-047.7 — delivery=net), agent không tham gia. Agent
// chỉ cầm việc này khi backend không với tới máy in: backend trên cloud (NAT
// chặn), hoặc máy in nằm khác VLAN.
type netPrinter struct {
	addr string
}

func (p *netPrinter) Write(b []byte) error {
	addr := p.addr
	// Cho phép cấu hình chỉ ghi IP — cổng 9100 là mặc định của mọi máy in raw.
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, defaultRawPort)
	}

	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("không kết nối được máy in mạng %s — kiểm tra dây mạng, IP và nguồn máy in: %w", addr, err)
	}
	defer conn.Close()

	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return fmt.Errorf("đặt hạn ghi %s: %w", addr, err)
	}

	// net.Conn.Write đã ghi-cho-tới-hết hoặc trả lỗi, không cần vòng lặp như
	// device node.
	if _, err := conn.Write(b); err != nil {
		return fmt.Errorf("gửi tới máy in mạng %s: %w", addr, err)
	}
	return nil
}

func (p *netPrinter) String() string { return "net:" + p.addr }
