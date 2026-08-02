// Command trcf-print-agent — in bill/phiếu bếp cho TRCF ERP.
//
// Bốn lệnh, theo đúng thứ tự người lắp đặt dùng:
//
//	list    xem máy in nào đang cắm vào máy này
//	test    in thử + đá két  (KHÔNG cần ERP, không cần ghép nối)
//	pair    ghép với ERP bằng mã 6 số rồi cài dịch vụ nền
//	run     vòng lặp chính (dịch vụ nền gọi)
//
// `test` cố ý chạy được TRƯỚC `pair`: lắp máy ở quán thì việc đầu tiên là biết
// máy in có sống không, không nên phụ thuộc vào việc ERP đã sẵn sàng hay chưa.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nguyentrucanhtuan/trcf_print_agent/internal/config"
	"github.com/nguyentrucanhtuan/trcf_print_agent/internal/escpos"
	"github.com/nguyentrucanhtuan/trcf_print_agent/internal/pair"
	"github.com/nguyentrucanhtuan/trcf_print_agent/internal/printer"
	"github.com/nguyentrucanhtuan/trcf_print_agent/internal/service"
	"github.com/nguyentrucanhtuan/trcf_print_agent/internal/transport"
)

// version — do build gắn: -ldflags "-X main.version=v0.1.0"
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "list":
		err = cmdList()
	case "test":
		err = cmdTest(os.Args[2:])
	case "pair":
		err = cmdPair(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("trcf-print-agent", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "Lệnh không hợp lệ: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "Lỗi:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`trcf-print-agent — in bill/phiếu bếp cho TRCF ERP

  trcf-print-agent list
      Xem máy in USB nào đang cắm vào máy này.

  trcf-print-agent test [--device /dev/usb/lp0] [--net 192.168.1.60:9100] [--drawer]
      In thử một tờ và (nếu có --drawer) đá ngăn kéo tiền.
      Không cần ERP, không cần ghép nối.

  trcf-print-agent pair --server http://192.168.1.50 --code 123456 [--device ...]
      Ghép với ERP, lưu cấu hình, cài dịch vụ chạy nền, in tờ xác nhận.

  trcf-print-agent run [--config /etc/trcf-print-agent/config.json]
      Vòng lặp chính. Dịch vụ nền gọi lệnh này.
`)
}

// ─────────────────────────────────────────────────────────────────────────────
// list — trả lời "máy nào đang cắm"
// ─────────────────────────────────────────────────────────────────────────────

func cmdList() error {
	found, err := printer.Discover()
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Println("Không thấy máy in USB nào.")
		fmt.Println()
		fmt.Println("Kiểm tra theo thứ tự:")
		fmt.Println("  1. Máy in đã bật nguồn và dây USB đã cắm chắc")
		fmt.Println("  2. Nhân đã nạp driver máy in:   lsmod | grep usblp")
		fmt.Println("     chưa có thì nạp tay:          sudo modprobe usblp")
		fmt.Println("  3. Máy có nhận thiết bị USB:     lsusb")
		return nil
	}

	fmt.Printf("Thấy %d máy in:\n\n", len(found))
	for i, f := range found {
		fmt.Printf("  [%d] %s\n", i+1, f.Label())
		if f.IDs != "" {
			fmt.Printf("      mã thiết bị: %s\n", f.IDs)
		}
	}
	fmt.Println()
	fmt.Println("In thử:  trcf-print-agent test --device", found[0].Path)

	// Nhiều hơn một máy in thì tên /dev/usb/lpN có thể HOÁN ĐỔI sau khi khởi
	// động lại — nhắc ngay tại đây, vì lúc đó người lắp đang có đủ thông tin
	// (mã thiết bị) để làm udev rule.
	if len(found) > 1 {
		fmt.Println()
		fmt.Println("⚠ Có nhiều hơn một máy in: tên /dev/usb/lpN có thể đổi chỗ cho nhau")
		fmt.Println("  sau khi khởi động lại máy. Nên cố định tên bằng udev rule —")
		fmt.Println("  xem mục \"Cố định tên máy in\" trong README.")
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// test — in thử, không cần ERP
// ─────────────────────────────────────────────────────────────────────────────

func cmdTest(args []string) error {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	device := fs.String("device", "", "đường dẫn máy in USB, vd /dev/usb/lp0")
	netAddr := fs.String("net", "", "địa chỉ máy in mạng, vd 192.168.1.60:9100")
	drawer := fs.Bool("drawer", false, "đá ngăn kéo tiền sau khi in")
	drawerPin := fs.Int("drawer-pin", 0, "chân RJ11 đá két: 0 = chân 2 (đa số), 1 = chân 5")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := resolveTarget(*device, *netAddr)
	if err != nil {
		return err
	}

	p, err := printer.Open(cfg)
	if err != nil {
		return err
	}

	// Gộp trang thử + lệnh đá két vào MỘT lần ghi.
	//
	// Không tách hai lần Write: với máy in LAN, mỗi lần Write là một kết nối TCP
	// mới, mà nhiều máy in giá rẻ chỉ chấp nhận một kết nối tại một thời điểm và
	// chưa kịp nhả socket → lần thứ hai bị từ chối, người lắp tưởng két hỏng.
	payload := escpos.TestPage(version, cfg.Target)
	if *drawer {
		payload = append(payload, escpos.KickDrawer(*drawerPin)...)
	}

	fmt.Printf("Đang in thử ra %s ...\n", cfg.Target)
	if err := p.Write(payload); err != nil {
		return err
	}
	fmt.Println("✓ Đã gửi trang in thử.")

	if *drawer {
		fmt.Printf("✓ Đã gửi kèm lệnh mở két (chân %d).\n", *drawerPin)
		fmt.Println("  Két không bật? Thử chân còn lại: --drawer-pin", 1-*drawerPin)
	}

	fmt.Println()
	fmt.Println("Nhìn tờ giấy vừa ra và kiểm 3 điều:")
	fmt.Println("  • Giấy có ĐỨT RỜI không          → lệnh cắt chạy đúng")
	fmt.Println("  • Dòng \"Tiếng Việt CÓ dấu\"       → ra chữ đúng hay ký tự rác")
	fmt.Println("  • Hàng số 32/48 ký tự có bị xuống dòng không → khổ giấy 58mm hay 80mm")
	return nil
}

// resolveTarget chọn máy in cho `test`.
//
// Nếu người dùng không chỉ định gì và máy chỉ có ĐÚNG MỘT máy in thì tự chọn
// nó — trường hợp phổ biến nhất ở quán, đỡ một bước gõ. Nhiều hơn một thì bắt
// chọn tường minh, tuyệt đối không đoán: in nhầm ra máy in bếp lúc lắp đặt là
// kiểu lỗi làm người ta mất niềm tin ngay từ đầu.
func resolveTarget(device, netAddr string) (printer.Config, error) {
	switch {
	case device != "" && netAddr != "":
		return printer.Config{}, errors.New("chỉ được chọn một trong --device hoặc --net")
	case netAddr != "":
		return printer.Config{Kind: printer.KindNet, Target: netAddr}, nil
	case device != "":
		return printer.Config{Kind: printer.KindUSB, Target: device}, nil
	}

	found, err := printer.Discover()
	if err != nil {
		return printer.Config{}, err
	}
	switch len(found) {
	case 0:
		return printer.Config{}, errors.New("không thấy máy in USB nào — chạy `trcf-print-agent list` để chẩn đoán, hoặc dùng --net cho máy in mạng")
	case 1:
		fmt.Printf("Tự chọn máy in duy nhất: %s\n", found[0].Label())
		return printer.Config{Kind: printer.KindUSB, Target: found[0].Path}, nil
	default:
		var b strings.Builder
		b.WriteString("có nhiều máy in, hãy chỉ rõ bằng --device:\n")
		for _, f := range found {
			fmt.Fprintf(&b, "  --device %s   (%s)\n", f.Path, f.Label())
		}
		return printer.Config{}, errors.New(b.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// pair — ghép với ERP
// ─────────────────────────────────────────────────────────────────────────────

func cmdPair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	server := fs.String("server", "", "địa chỉ ERP, vd http://192.168.1.50")
	code := fs.String("code", "", "mã ghép 6 số lấy từ ERP")
	device := fs.String("device", "", "đường dẫn máy in USB (bỏ trống = tự dò)")
	netAddr := fs.String("net", "", "địa chỉ máy in mạng thay cho USB")
	drawerPin := fs.Int("drawer-pin", 0, "chân RJ11 đá két: 0 = chân 2 (đa số), 1 = chân 5")
	cfgPath := fs.String("config", config.DefaultPath, "nơi lưu cấu hình")
	noService := fs.Bool("no-service", false, "chỉ lưu cấu hình, không cài dịch vụ nền")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Hỏi ngay tại chỗ khi thiếu — người lắp đặt chạy tay thì không nên bắt họ
	// nhớ đúng cú pháp cờ.
	if *server == "" {
		*server = prompt("Địa chỉ ERP (vd http://192.168.1.50): ")
	}
	if *code == "" {
		*code = prompt("Mã ghép 6 số (lấy ở ERP → Máy in): ")
	}
	if *server == "" || *code == "" {
		return errors.New("thiếu địa chỉ ERP hoặc mã ghép")
	}

	target, err := resolveTarget(*device, *netAddr)
	if err != nil {
		return err
	}

	// In thử TRƯỚC khi ghép: nếu máy in hỏng thì biết ngay, và chưa tạo ra một
	// bản ghi "đã ghép" sai bên ERP để sau phải đi dọn.
	p, err := printer.Open(target)
	if err != nil {
		return err
	}
	fmt.Println("Đang in thử trước khi ghép ...")
	if err := p.Write(escpos.TestPage(version, target.Target)); err != nil {
		return fmt.Errorf("máy in chưa in được, dừng ghép nối: %w", err)
	}
	fmt.Println("✓ Máy in nhận lệnh.")

	host, _ := os.Hostname()
	res, err := pair.Do(context.Background(), *server, pair.Request{
		Code:        strings.TrimSpace(*code),
		Hostname:    host,
		OS:          "linux",
		PrinterDesc: target.Target,
		Version:     version,
	})
	if err != nil {
		return err
	}

	cfg := &config.Config{
		ServerURL: strings.TrimRight(*server, "/"),
		Token:     res.Token,
		PrinterID: res.PrinterID,
		Printer:   target,
		DrawerPin: *drawerPin,
	}
	if err := config.Save(*cfgPath, cfg); err != nil {
		return err
	}
	fmt.Printf("✓ Đã ghép với máy in \"%s\" (id %d). Cấu hình: %s\n",
		res.PrinterName, res.PrinterID, *cfgPath)

	if *noService {
		fmt.Println("Bỏ qua cài dịch vụ (--no-service). Chạy tay:  trcf-print-agent run")
		return nil
	}
	if err := service.Install(); err != nil {
		// Ghép nối ĐÃ xong và cấu hình đã lưu — cài dịch vụ hỏng không được xoá
		// công đó, chỉ báo để chạy tay.
		fmt.Fprintln(os.Stderr, "⚠ Chưa cài được dịch vụ nền:", err)
		fmt.Fprintln(os.Stderr, "  Cấu hình vẫn đã lưu. Chạy tay:  trcf-print-agent run")
		return nil
	}
	fmt.Println("✓ Đã cài dịch vụ nền (tự chạy lại khi khởi động máy).")
	fmt.Println("  Xem nhật ký:  journalctl -u trcf-print-agent -f")
	return nil
}

func prompt(label string) string {
	fmt.Print(label)
	s, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// ─────────────────────────────────────────────────────────────────────────────
// run — vòng lặp chính
// ─────────────────────────────────────────────────────────────────────────────

const (
	backoffMin     = 2 * time.Second
	backoffMax     = 60 * time.Second
	heartbeatEvery = 30 * time.Second
)

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath, "đường dẫn cấu hình")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	p, err := printer.Open(cfg.Printer)
	if err != nil {
		return err
	}
	client := transport.New(cfg.ServerURL, cfg.Token, version)

	// Ctrl-C / systemd stop → dừng gọn, không cắt ngang giữa lúc đang ghi ra
	// máy in (dở dang = bill cụt).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logf("khởi động %s — ERP %s, máy in %s (id %d)", version, cfg.ServerURL, cfg.Printer.Target, cfg.PrinterID)

	go heartbeatLoop(ctx, client, p, cfg)

	backoff := backoffMin
	for {
		if ctx.Err() != nil {
			logf("dừng theo yêu cầu")
			return nil
		}

		job, err := client.NextJob(ctx)
		switch {
		case err == nil:
			handleJob(ctx, client, p, job)
			backoff = backoffMin // gọi được ERP = mạng lành, reset chờ
			continue
		case errors.Is(err, transport.ErrNoJob):
			// Bình thường: quán chưa có việc in. Hỏi lại NGAY, không chờ.
			backoff = backoffMin
			continue
		case ctx.Err() != nil:
			return nil
		}

		// Chỉ tới đây mới là lỗi thật (mất mạng, ERP tắt, token hỏng).
		logf("lỗi: %v — thử lại sau %s", err, backoff)
		if !sleepCtx(ctx, backoff) {
			return nil
		}
		backoff = min(backoff*2, backoffMax)
	}
}

// handleJob in một việc rồi báo kết quả.
//
// Điểm cốt yếu: lỗi in KHÔNG làm agent chết và KHÔNG bị nuốt — nó được BÁO
// NGƯỢC về ERP để hiện ở quầy (PRD FR-047.9/.16). Máy in hết giấy thì nhân viên
// phải thấy, chứ không phải phiếu lặng lẽ biến mất.
func handleJob(ctx context.Context, client *transport.Client, p printer.Printer, job *transport.Job) {
	payload, err := job.Payload()
	if err == nil {
		logf("job %d: in %d byte", job.ID, len(payload))
		err = p.Write(payload)
	}

	if err != nil {
		logf("job %d: LỖI %v", job.ID, err)
	} else {
		logf("job %d: xong", job.ID)
	}

	if rerr := client.ReportResult(ctx, job.ID, err); rerr != nil {
		// In được mà báo không được là tình huống xấu: ERP tưởng hỏng và sẽ cho
		// in lại → ra hai tờ. Ghi rõ để còn lần theo khi có khiếu nại.
		logf("job %d: KHÔNG báo được kết quả về ERP: %v", job.ID, rerr)
	}
}

// heartbeatLoop báo agent còn sống + cổng máy in có mở được không.
func heartbeatLoop(ctx context.Context, client *transport.Client, p printer.Printer, cfg *config.Config) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Gửi buffer RỖNG để thăm cổng: mở được device/kết nối được TCP là
			// máy in sống, mà không tốn một milimet giấy nào.
			portErr := p.Write(nil)
			msg := ""
			if portErr != nil {
				msg = portErr.Error()
			}
			if err := client.Heartbeat(ctx, portErr == nil, msg); err != nil {
				logf("heartbeat lỗi: %v", err)
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func logf(format string, a ...any) {
	fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}
