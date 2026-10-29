// Command trcf-bridge — in bill/phiếu bếp cho TRCF ERP.
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

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/core"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/escpos"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/service"
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
		fmt.Println("trcf-bridge", version)
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
	fmt.Print(`trcf-bridge — in bill/phiếu bếp cho TRCF ERP

  trcf-bridge list
      Xem máy in USB nào đang cắm vào máy này.

  trcf-bridge test [--device /dev/usb/lp0] [--net 192.168.1.60:9100] [--drawer]
      In thử một tờ và (nếu có --drawer) đá ngăn kéo tiền.
      Không cần ERP, không cần ghép nối.

  trcf-bridge pair --server http://192.168.1.50 --code 123456 [--device ...]
      Ghép với ERP, lưu cấu hình, cài dịch vụ chạy nền, in tờ xác nhận.

  trcf-bridge run [--config /etc/trcf-bridge/config.json]
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
		if f.Identity.IDs != "" {
			fmt.Printf("      mã thiết bị: %s\n", f.Identity.IDs)
		}
	}
	fmt.Println()
	fmt.Println("In thử:  trcf-bridge test --device", found[0].Path)

	// Tên /dev/usb/lpN vẫn đổi chỗ sau mỗi lần khởi động, nhưng agent nay tự tìm
	// lại máy theo DANH TÍNH nên chuyện đó không còn là vấn đề — KHÔNG nhắc udev
	// nữa, lời khuyên thừa làm người lắp mất công vô ích.
	//
	// Chỉ còn đúng một ca đáng cảnh báo: máy in không khai số sê-ri. Lúc đó danh
	// tính rơi về mã thiết bị + vị trí cổng USB, nên cắm sang cổng khác là mất
	// khớp — người lắp cần biết để đừng đổi cổng, hoặc để dán nhãn lên cổng.
	var yeu []string
	for _, f := range found {
		if f.Identity.Serial == "" {
			yeu = append(yeu, f.Path)
		}
	}
	if len(yeu) > 0 {
		fmt.Println()
		fmt.Printf("⚠ Máy in không khai số sê-ri: %s\n", strings.Join(yeu, ", "))
		fmt.Println("  Agent sẽ nhận diện bằng mã thiết bị + VỊ TRÍ CỔNG USB, nên nếu cắm")
		fmt.Println("  sang cổng khác thì phải chọn lại thiết bị bên ERP. Dán nhãn lên cổng.")
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
		// Máy in LAN đã có danh tính bền vững sẵn: IP:port. Không cần gì thêm.
		return printer.Config{Kind: printer.KindNet, Target: netAddr}, nil
	case device != "":
		return usbConfig(device, nil), nil
	}

	found, err := printer.Discover()
	if err != nil {
		return printer.Config{}, err
	}
	switch len(found) {
	case 0:
		return printer.Config{}, errors.New("không thấy máy in USB nào — chạy `trcf-bridge list` để chẩn đoán, hoặc dùng --net cho máy in mạng")
	case 1:
		fmt.Printf("Tự chọn máy in duy nhất: %s\n", found[0].Label())
		return usbConfig(found[0].Path, &found[0].Identity), nil
	default:
		var b strings.Builder
		b.WriteString("có nhiều máy in, hãy chỉ rõ bằng --device:\n")
		for _, f := range found {
			fmt.Fprintf(&b, "  --device %s   (%s)\n", f.Path, f.Label())
		}
		return printer.Config{}, errors.New(b.String())
	}
}

// usbConfig dựng cấu hình máy in USB kèm DANH TÍNH của thiết bị đang nằm ở đó.
//
// Đọc danh tính ngay lúc chọn (chứ không để lần in đầu mới đọc) vì đây là lúc
// người lắp đang đứng cạnh máy: sai thì họ sửa được ngay. Đọc không ra thì vẫn
// ghép được theo đường dẫn — máy in cũ kỹ không khai gì cũng phải dùng được,
// chỉ là mất khả năng tự tìm lại khi số lpN đổi.
func usbConfig(path string, known *printer.Identity) printer.Config {
	cfg := printer.Config{Kind: printer.KindUSB, Target: path}
	if known != nil && !known.IsZero() {
		id := *known
		cfg.Identity = &id
		return cfg
	}
	if id, err := printer.IdentityForPath(path); err == nil {
		cfg.Identity = &id
	} else {
		fmt.Printf("⚠ Không đọc được danh tính của %s (%v).\n", path, err)
		fmt.Println("  Vẫn ghép được, nhưng nếu số /dev/usb/lpN đổi sau khi khởi động thì phải ghép lại.")
	}
	return cfg
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

	// Thứ tự an toàn (in thử → kiểm ghi được cấu hình → gọi ERP → lưu) nằm ở
	// `core.Pair`, dùng chung với màn thiết lập của Android.
	host, _ := os.Hostname()
	params := core.PairParams{
		ConfigPath: *cfgPath,
		Server:     *server,
		Code:       *code,
		Target:     target,
		Hostname:   host,
		OS:         "linux",
		Version:    version,
	}
	// CHỈ ghi đè chân đá két khi người lắp thật sự gõ cờ — xem `PairParams.DrawerPin`.
	if drawerPinSet(fs) {
		params.DrawerPin = drawerPin
	}
	res, cfg, err := core.Pair(context.Background(), params, func(line string) { fmt.Println(line) })
	if err != nil {
		return err
	}
	fmt.Printf("✓ Đã ghép với máy in \"%s\" (id %d). Cấu hình: %s\n",
		res.PrinterName, res.PrinterID, *cfgPath)
	if len(cfg.Printers) > 1 {
		fmt.Printf("  Máy này đang cầm %d máy in — vẫn chỉ một dịch vụ nền.\n", len(cfg.Printers))
	}

	if *noService {
		fmt.Println("Bỏ qua cài dịch vụ (--no-service). Chạy tay:  trcf-bridge run")
		return nil
	}
	// Trong container thì KHÔNG có systemd, và cũng không cần: `restart: unless-stopped`
	// của Docker làm đúng việc tự chạy lại. Không nhận ra chỗ này thì người lắp thấy
	// dòng "⚠ Chưa cài được dịch vụ nền" ngay sau khi ghép THÀNH CÔNG — đọc như hỏng.
	if service.InContainer() {
		fmt.Println("Đang chạy trong container — không cài dịch vụ nền (Docker tự chạy lại).")
		fmt.Println("  Bật lên bằng:  docker compose up -d")
		return nil
	}
	if err := service.Install(); err != nil {
		// Ghép nối ĐÃ xong và cấu hình đã lưu — cài dịch vụ hỏng không được xoá
		// công đó, chỉ báo để chạy tay.
		fmt.Fprintln(os.Stderr, "⚠ Chưa cài được dịch vụ nền:", err)
		fmt.Fprintln(os.Stderr, "  Cấu hình vẫn đã lưu. Chạy tay:  trcf-bridge run")
		return nil
	}
	fmt.Println("✓ Đã cài dịch vụ nền (tự chạy lại khi khởi động máy).")
	fmt.Println("  Xem nhật ký:  journalctl -u trcf-bridge -f")
	return nil
}

// drawerPinSet cho biết người dùng có gõ `--drawer-pin` hay không.
//
// `flag` không phân biệt "không gõ" với "gõ đúng giá trị mặc định", nên phải hỏi
// bộ cờ xem nó đã được đặt chưa.
func drawerPinSet(fs *flag.FlagSet) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "drawer-pin" {
			set = true
		}
	})
	return set
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

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath, "đường dẫn cấu hình")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Ctrl-C / systemd stop → dừng gọn, không cắt ngang giữa lúc đang ghi ra
	// máy in (dở dang = bill cụt).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return core.Run(ctx, *cfgPath, version)
}
