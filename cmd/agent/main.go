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
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/escpos"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/pair"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/service"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/transport"
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

// printerDesc — chuỗi mô tả gửi lên ERP để hiện ở màn Máy in.
//
// Trước đây gửi ĐƯỜNG DẪN ("/dev/usb/lp2"), nên ERP chưa từng nhìn thấy tên máy
// in nào. Nay gửi danh tính đọc được: "Printer POS-80 · EPSON · SN 1C79EC420000".
func printerDesc(cfg printer.Config) string {
	if id := cfg.Identity; id != nil && !id.IsZero() {
		return id.Label()
	}
	return cfg.Target
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

	// Máy này đã ghép lần nào chưa. Có rồi thì gửi kèm token cũ để ERP GẮN THÊM
	// máy in vào chính máy quầy đó, thay vì đẻ ra một máy quầy thứ hai với token
	// riêng — tức là quay về đúng mô hình cũ mà không ai nhận ra, vì vẫn in được.
	//
	// Phân biệt "chưa có tệp" với "tệp có mà đọc không xong": dựng Config rỗng cho
	// MỌI lỗi sẽ vứt sạch token và danh sách máy in đã ghép chỉ vì một trường thiếu
	// (ai đó sửa tay, hoặc mất điện lúc ghi). Các máy in kia vẫn còn liên kết bên ERP
	// nhưng biến mất khỏi cấu hình ở đây — chúng lặng lẽ ngừng in mà màn hình vẫn xanh.
	cfg, err := config.LoadForPairing(*cfgPath)
	if err != nil {
		return err
	}

	// Thử ghi được cấu hình TRƯỚC khi gọi ERP.
	//
	// `pair` nay đổi được chủ của một máy in (ON CONFLICT chuyển `agent_id`). Nếu gọi
	// ERP xong mới phát hiện không ghi nổi `/etc` vì quên `sudo`, thì bên ERP máy in
	// đã chuyển sang máy quầy này rồi mà máy này lại không có token để chạy — máy in
	// đang in tốt bỗng thuộc về một agent không tồn tại và ngừng in trong im lặng.
	if err := config.CheckWritable(*cfgPath); err != nil {
		return err
	}

	host, _ := os.Hostname()
	res, err := pair.Do(context.Background(), *server, pair.Request{
		Code:        strings.TrimSpace(*code),
		Hostname:    host,
		OS:          "linux",
		PrinterDesc: printerDesc(target),
		Version:     version,
	}, cfg.Token)
	if err != nil {
		return err
	}

	cfg.ServerURL = strings.TrimRight(*server, "/")
	cfg.Token = res.Token
	// CHỈ ghi đè chân đá két khi người lắp thật sự gõ cờ. Ghi đè vô điều kiện thì
	// quán dùng két chân 5 (`--drawer-pin 1`) sẽ mất chức năng mở két ngay lúc ghép
	// THÊM máy in bếp — vì lệnh ghép thêm không ai gõ lại cờ đó. Triệu chứng là "két
	// tự dưng không bật", không đời nào ai lần ra được nguyên nhân là máy in bếp.
	if drawerPinSet(fs) {
		cfg.DrawerPin = *drawerPin
	}
	cfg.Upsert(config.Binding{PrinterID: res.PrinterID, Printer: target})
	if err := config.Save(*cfgPath, cfg); err != nil {
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

	// Cho package printer nói ra chuyện nó thấy (máy in đổi đường dẫn, khớp bằng
	// khoá lùi). Chỉ `run` gắn — `list`/`test` giữ màn hình sạch.
	printer.Logf = logf

	// Tự chữa cấu hình đời cũ TRƯỚC khi mở máy in: biến "/dev/usb/lp2" thành một
	// danh tính bền vững, để quán đã cài không phải khai lại gì.
	if healSelf(cfg, *cfgPath) {
		logf("đã ghi danh tính máy in vào %s", *cfgPath)
	}
	for _, w := range cfg.Warnings() {
		logf("⚠ %s", w)
	}

	// Gom máy in ERP theo THIẾT BỊ VẬT LÝ. `Open` cố ý không kiểm tra thiết bị có
	// tồn tại hay không (xem `openUSB`), nên một máy in đang tắt cũng không cản
	// agent khởi động — lỗi sẽ hiện ra ở từng lần in.
	//
	// Một mục cấu hình sai (gõ nhầm đường dẫn máy in tem) KHÔNG được làm cả quầy
	// không in được gì: ghi lại rồi đi tiếp, chỉ bỏ cuộc khi không mở nổi máy nào.
	groups, routes, err := groupByDevice(cfg)
	if err != nil {
		return err
	}
	client := transport.New(cfg.ServerURL, cfg.Token, version)

	// Ctrl-C / systemd stop → dừng gọn, không cắt ngang giữa lúc đang ghi ra
	// máy in (dở dang = bill cụt).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logf("khởi động %s — ERP %s, %d máy in trên %d thiết bị", version, cfg.ServerURL, len(cfg.Printers), len(groups))
	for _, b := range cfg.Printers {
		logf("  máy in id %d → %s", b.PrinterID, describeBinding(b))
	}

	// MỘT thợ cho MỖI THIẾT BỊ — không phải mỗi máy in ERP.
	//
	// ─────────────────────────────────────────────────────────────────────
	// Vì sao gom theo thiết bị: `usblp` chỉ cho MỘT tiến trình mở device node tại
	// một thời điểm. Quán khai một máy POS-80 làm cả "Bill quầy" lẫn "Phiếu bếp"
	// (ca dùng hợp lệ, xem `config.Warnings`) mà cho mỗi máy in một thợ thì hai
	// thợ sẽ tranh nhau cùng một cổng: một thợ thắng, thợ kia ăn EBUSY và lệnh in
	// HỎNG — mà câu lỗi lại nói "đang bị chương trình khác giữ, thường là CUPS",
	// đổ oan cho CUPS và đẩy người sửa đi sai hướng cả buổi.
	//
	// Vì sao vẫn phải nhiều thợ (không gộp hết vào vòng lặp chính): `Write` của
	// device node có thể TREO tới 15 giây khi máy in kẹt giấy. Một máy in bếp kẹt
	// giấy không được phép chặn bill cho khách đang đứng đợi — nên hai THIẾT BỊ
	// khác nhau vẫn chạy độc lập hoàn toàn.
	//
	// Đánh đổi phải nói trước với chủ quán: gộp hai vai trò vào MỘT máy vật lý thì
	// kẹt giấy chặn cả hai. Đó là vật lý, không phải phần mềm.
	// ─────────────────────────────────────────────────────────────────────
	for _, g := range groups {
		go deviceWorker(ctx, client, g)
	}

	go heartbeatLoop(ctx, client, groups, routes)

	backoff := backoffMin
	for {
		if ctx.Err() != nil {
			logf("dừng theo yêu cầu")
			return nil
		}

		job, err := client.NextJob(ctx)
		switch {
		case err == nil:
			dispatchJob(ctx, client, routes, job)
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

// healSelf nâng cấu hình đời cũ (chỉ có đường dẫn) lên danh tính bền vững.
//
// ─────────────────────────────────────────────────────────────────────────
// Đây là đường di trú cho 5 quán đã cài: agent bản mới đọc cấu hình bản cũ, tự
// đọc danh tính của thiết bị đang nằm ở đường dẫn ấy rồi ghi lại. Quán được cố
// định vĩnh viễn mà KHÔNG ai phải gõ gì, không phải SSH.
//
// Khi đường dẫn cũ KHÔNG còn thiết bị nào (đúng ca Gác Dero: cấu hình trỏ lp2,
// sau khởi động chỉ còn lp0/lp1) thì TUYỆT ĐỐI KHÔNG ĐOÁN — không được vơ lấy
// thiết bị còn lại, vì đoán sai chính là thứ đã in ra tờ giấy đầy chữ lệnh. Ghi
// nhật ký rồi để lệnh in báo hỏng, chờ người chọn lại bên ERP.
// ─────────────────────────────────────────────────────────────────────────
func healSelf(cfg *config.Config, path string) bool {
	changed := false
	for i := range cfg.Printers {
		b := &cfg.Printers[i]
		if b.Printer.Kind != printer.KindUSB {
			continue // máy in LAN: IP:port vốn đã bền vững
		}
		if id := b.Printer.Identity; id != nil && !id.IsZero() {
			continue
		}
		id, err := printer.IdentityForPath(b.Printer.Target)
		if err != nil {
			logf("máy in id %d: chưa suy được danh tính từ %s (%v) — vào ERP mục Máy in chọn lại thiết bị",
				b.PrinterID, b.Printer.Target, err)
			continue
		}
		b.Printer.Identity = &id
		logf("máy in id %d: %s → %s", b.PrinterID, b.Printer.Target, id.Label())
		changed = true
	}
	if !changed {
		return false
	}
	if err := config.Save(path, cfg); err != nil {
		// Ghi hỏng KHÔNG được cản việc in: danh tính đã nằm trong bộ nhớ, phiên
		// này chạy đúng. Chỉ là lần khởi động sau phải suy lại từ đầu.
		logf("⚠ không ghi được danh tính vào %s: %v — phiên này vẫn chạy đúng", path, err)
		return false
	}
	return true
}

// deviceGroup — một THIẾT BỊ vật lý và mọi máy in ERP trỏ vào nó.
//
// Nhiều máy in ERP dùng chung một thiết bị là ca hợp lệ (một POS-80 in cả bill
// lẫn phiếu bếp). Chúng dùng chung MỘT hàng đợi và MỘT thợ, vì `usblp` chỉ cho
// một tiến trình mở device node tại một thời điểm.
type deviceGroup struct {
	key        string // khoá danh tính, KHÔNG phải đường dẫn
	label      string // để đọc nhật ký
	printer    printer.Printer
	printerIDs []int
	jobs       chan *transport.Job
}

// groupByDevice gom cấu hình thành các nhóm theo thiết bị vật lý.
//
// Trả kèm bảng tra printer_id → nhóm, vì lệnh in từ ERP mang printer_id chứ
// không biết gì về thiết bị.
func groupByDevice(cfg *config.Config) (map[string]*deviceGroup, map[int]*deviceGroup, error) {
	groups := make(map[string]*deviceGroup)
	routes := make(map[int]*deviceGroup, len(cfg.Printers))

	for _, b := range cfg.Printers {
		key := b.DeviceKey()
		g, ok := groups[key]
		if !ok {
			p, err := printer.Open(b.Printer)
			if err != nil {
				logf("BỎ QUA máy in id %d (%s): %v", b.PrinterID, b.Printer.Target, err)
				continue
			}
			g = &deviceGroup{
				key:     key,
				label:   describeBinding(b),
				printer: p,
				// Đệm để vòng lặp chính không phải chờ thợ; đầy hàng nghĩa là
				// thiết bị đó đang tắc thật, và lúc đó báo hỏng còn hơn giữ việc
				// im lặng.
				jobs: make(chan *transport.Job, 16),
			}
			groups[key] = g
		}
		g.printerIDs = append(g.printerIDs, b.PrinterID)
		routes[b.PrinterID] = g
	}

	if len(groups) == 0 {
		return nil, nil, fmt.Errorf("không mở được máy in nào trong cấu hình — chạy `list` để xem thiết bị đang cắm")
	}
	return groups, routes, nil
}

// describeBinding — dòng chữ cho người đọc nhật ký.
//
// Ưu tiên danh tính vì đó mới là chiếc máy; đường dẫn chỉ là chỗ nó đang nằm
// hôm nay và sẽ đổi sau lần khởi động tới.
func describeBinding(b config.Binding) string {
	if id := b.Printer.Identity; id != nil && !id.IsZero() {
		return id.Label()
	}
	return b.Printer.Target
}

// handleJob in một việc rồi báo kết quả.
//
// Điểm cốt yếu: lỗi in KHÔNG làm agent chết và KHÔNG bị nuốt — nó được BÁO
// NGƯỢC về ERP để hiện ở quầy (PRD FR-047.9/.16). Máy in hết giấy thì nhân viên
// phải thấy, chứ không phải phiếu lặng lẽ biến mất.
func dispatchJob(
	ctx context.Context,
	client *transport.Client,
	routes map[int]*deviceGroup,
	job *transport.Job,
) {
	// Máy in vừa bị gỡ ở ERP nhưng lệnh cũ còn trong hàng đợi. Báo hỏng chứ KHÔNG
	// im lặng bỏ qua: im lặng thì job nằm `sent` tới khi bị thu hồi rồi giao lại,
	// lặp vô hạn mà không ai thấy lý do. Ghép lại rồi khởi động lại là xong.
	g, ok := routes[job.PrinterID]
	if !ok {
		reportErr(ctx, client, job.ID, fmt.Errorf(
			"máy tính này không cầm máy in id %d — chạy `pair` lại rồi khởi động lại dịch vụ",
			job.PrinterID))
		return
	}

	// Gửi KHÔNG chờ. Hàng đầy nghĩa là thiết bị đó đang tắc thật (kẹt giấy, treo
	// giữa lần ghi) — báo hỏng để ERP tính lượt thử lại, còn hơn chặn vòng lặp và
	// kéo theo mọi thiết bị khác.
	select {
	case g.jobs <- job:
	default:
		reportErr(ctx, client, job.ID, fmt.Errorf(
			"máy in id %d đang tắc, hàng đợi tại quầy đã đầy — kiểm tra giấy và dây",
			job.PrinterID))
	}
}

// deviceWorker in tuần tự cho ĐÚNG MỘT thiết bị vật lý.
//
// Tuần tự trong phạm vi một thiết bị là cố ý và nay là BẮT BUỘC: hai lệnh ghi
// chen nhau vào cùng device node thì một cái ăn EBUSY (usblp chỉ cho một tiến
// trình mở), hoặc tệ hơn là ra một tờ ký tự rác. Giữa các thiết bị thì độc lập
// hoàn toàn.
func deviceWorker(ctx context.Context, client *transport.Client, g *deviceGroup) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-g.jobs:
			payload, err := job.Payload()
			if err == nil {
				logf("job %d: in %d byte ra máy in id %d (%s)", job.ID, len(payload), job.PrinterID, g.label)
				err = g.printer.Write(payload)
			}
			if err != nil {
				logf("job %d: LỖI %v", job.ID, err)
			} else {
				logf("job %d: xong", job.ID)
			}
			if rerr := client.ReportResult(ctx, job.ID, err); rerr != nil {
				// In được mà báo không được là tình huống xấu: ERP tưởng hỏng và sẽ
				// cho in lại → ra hai tờ. Ghi rõ để còn lần theo khi có khiếu nại.
				logf("job %d: KHÔNG báo được kết quả về ERP: %v", job.ID, rerr)
			}
		}
	}
}

func reportErr(ctx context.Context, client *transport.Client, jobID int64, cause error) {
	logf("job %d: %v", jobID, cause)
	if rerr := client.ReportResult(ctx, jobID, cause); rerr != nil {
		logf("job %d: KHÔNG báo được kết quả về ERP: %v", jobID, rerr)
	}
}

// heartbeatLoop báo máy quầy còn sống + thăm cổng TỪNG THIẾT BỊ.
//
// Thăm theo thiết bị chứ không theo máy in ERP: hai vai trò dùng chung một máy
// mà thăm hai lần thì cú thăm này sẽ va EBUSY với cú in kia, đẻ ra báo động giả
// "máy in mất kết nối" ngay giữa lúc máy in đang chạy tốt. Thăm một lần rồi
// báo cùng một kết quả cho mọi máy in ERP trỏ vào nó.
func heartbeatLoop(
	ctx context.Context,
	client *transport.Client,
	groups map[string]*deviceGroup,
	routes map[int]*deviceGroup,
) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Thăm mỗi thiết bị đúng một lần. Gửi buffer RỖNG: mở được device /
			// kết nối được TCP là máy in sống, mà không tốn một milimet giấy nào.
			probed := make(map[string]error, len(groups))
			for key, g := range groups {
				probed[key] = g.printer.Write(nil)
			}

			// Đi theo thứ tự printer_id đã sắp để nhật ký đọc dễ; map trong Go
			// trả thứ tự ngẫu nhiên.
			ids := make([]int, 0, len(routes))
			for id := range routes {
				ids = append(ids, id)
			}
			sort.Ints(ids)

			status := make([]transport.PrinterStatus, 0, len(ids))
			for _, id := range ids {
				g := routes[id]
				portErr, ok := probed[g.key]
				if !ok {
					continue
				}
				s := transport.PrinterStatus{PrinterID: id, PortOK: portErr == nil}
				if portErr != nil {
					s.LastErr = portErr.Error()
				}
				status = append(status, s)
			}
			if err := client.Heartbeat(ctx, status); err != nil {
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
