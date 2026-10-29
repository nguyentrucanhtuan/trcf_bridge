// Package core — vòng chạy của cầu nối, DÙNG CHUNG cho mọi nền tảng.
//
// Long-poll lấy lệnh in, một hàng đợi cho mỗi THIẾT BỊ vật lý, nhịp sống 30 giây,
// đồng bộ bản gán từ web rồi nạp lại không cần khởi động lại. Không phụ thuộc hệ điều
// hành: phần riêng nền tảng (ghi ra máy in, dò máy in) nằm sau `printer.Printer` và
// `printer.USBBackend`.
//
// Tách khỏi `cmd/agent` để Android dùng CHÍNH đoạn code này thay vì chép lại. Các
// luật ở đây (0 khớp → báo lỗi, bản gán rỗng → giữ nguyên, chờ lệnh in dở trước khi
// nạp lại) đều sinh từ sự cố thật; hai bản chép sẽ lệch nhau.
package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nguyentrucanhtuan/trcf_bridge/internal/config"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/printer"
	"github.com/nguyentrucanhtuan/trcf_bridge/internal/transport"
)

const (
	backoffMin     = 2 * time.Second
	backoffMax     = 60 * time.Second
	heartbeatEvery = 30 * time.Second

	// Trần chờ lệnh in dở xong khi nạp lại bản gán. Phải RỘNG HƠN mức treo tệ nhất
	// của một cú ghi, nếu không thì chính chỗ này cắt ngang một tờ bill đang in và
	// ERP in lại tờ thứ hai — đúng thứ mà `writeGrace` sinh ra để chặn (xem nhật ký
	// dòng dưới: "lệnh đó có thể bị ERP cho in lại").
	//
	// 75 giây = 15 (mốc mềm) + 45 (hạn chờ thêm) + 15 dư. Bản trước để 30 giây, hồi
	// đó một cú ghi trần 15 giây nên 30 là "hơn gấp đôi"; nay trần là 60 nên giữ 30
	// hoá ra cắt vào GIỮA quãng chờ hợp lệ. Nạp lại bản gán là việc hiếm (đổi cấu
	// hình máy in), chờ thêm vài chục giây ở đó rẻ hơn một tờ bill sai.
	drainTimeout = 75 * time.Second
)

// Run là vòng chính của cầu nối: nạp cấu hình, chạy phiên, nạp lại khi ERP đổi bản
// gán — cho tới khi `ctx` bị huỷ. Dùng chung cho mọi nền tảng: Linux gọi từ lệnh
// `run` (systemd), Android gọi từ Foreground Service.
//
// `ctx` do bên gọi giữ vì mỗi nền tảng dừng theo cách riêng (SIGTERM ở Linux, dịch
// vụ bị huỷ ở Android); huỷ nó thì phiên dừng gọn, không cắt ngang một lần ghi.
func Run(ctx context.Context, cfgPath, version string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	// Cho package printer nói ra chuyện nó thấy (máy in đổi đường dẫn, khớp bằng
	// khoá lùi). Chỉ `run` gắn — `list`/`test` giữ màn hình sạch.
	printer.Logf = logf

	// Tự chữa cấu hình đời cũ TRƯỚC khi mở máy in: biến "/dev/usb/lp2" thành một
	// danh tính bền vững, để quán đã cài không phải khai lại gì.
	if healSelf(cfg, cfgPath) {
		logf("đã ghi danh tính máy in vào %s", cfgPath)
	}
	for _, w := range cfg.Warnings() {
		logf("⚠ %s", w)
	}

	client := transport.New(cfg.ServerURL, cfg.Token, version)

	// Vòng PHIÊN. Mỗi phiên là một bộ thợ ứng với một bản gán; đổi gán trên web thì
	// phiên hiện tại rút lui gọn rồi phiên mới dựng lại theo cấu hình vừa ghi (047f).
	//
	// Nạp lại TRONG tiến trình, không tự thoát để chờ systemd bật lại: agent còn được
	// chạy tay trong terminal lúc lắp đặt, và một tiến trình tự thoát ở đó là chết hẳn.
	for {
		reload, err := runSession(ctx, client, cfg, cfgPath, version)
		if err != nil || !reload {
			return err
		}
		fresh, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("nạp lại cấu hình sau khi ERP đổi bản gán: %w", err)
		}
		cfg = fresh
		logf("── nạp lại theo bản gán mới: %d máy in ──", len(cfg.Printers))
	}
}

// runSession chạy MỘT bản gán cho tới khi bị dừng (`reload=false`) hoặc tới khi ERP
// đổi bản gán (`reload=true`).
func runSession(
	ctx context.Context,
	client *transport.Client,
	cfg *config.Config,
	cfgPath string,
	version string,
) (bool, error) {
	// Gom máy in ERP theo THIẾT BỊ VẬT LÝ. `Open` cố ý không kiểm tra thiết bị có
	// tồn tại hay không (xem `openUSB`), nên một máy in đang tắt cũng không cản
	// agent khởi động — lỗi sẽ hiện ra ở từng lần in.
	//
	// Một mục cấu hình sai (gõ nhầm đường dẫn máy in tem) KHÔNG được làm cả quầy
	// không in được gì: ghi lại rồi đi tiếp, chỉ bỏ cuộc khi không mở nổi máy nào.
	groups, routes, err := groupByDevice(cfg)
	if err != nil {
		return false, err
	}

	sctx, scancel := context.WithCancel(ctx)
	defer scancel()

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
	var wg sync.WaitGroup
	for _, g := range groups {
		wg.Add(1)
		go func(g *deviceGroup) {
			defer wg.Done()
			deviceWorker(sctx, client, g)
		}(g)
	}

	// Báo về khi ERP đổi bản gán. Đệm 1 để nhịp sống không bị chặn nếu vòng chính đang
	// treo trong một cú long-poll.
	reload := make(chan struct{}, 1)
	go heartbeatLoop(sctx, client, groups, routes, cfgPath, reload)

	backoff := backoffMin
	for {
		if ctx.Err() != nil {
			logf("dừng theo yêu cầu")
			return false, nil
		}
		// Kiểm GIỮA hai cú long-poll, không cắt ngang cú đang chạy: cắt ngang thì có
		// cửa sổ mà ERP vừa giao một lệnh in cho một socket đã đóng → lệnh nằm `sent`
		// → hai phút sau bị thu hồi và IN LẠI TỜ THỨ HAI. Đổi lấy tối đa một nhịp
		// long-poll (25 giây) chậm trễ, đúng như spec đã hứa "ở nhịp poll kế".
		select {
		case <-reload:
			drainGroups(groups, &wg)
			return true, nil
		default:
		}

		job, err := client.NextJob(sctx)
		switch {
		case err == nil:
			dispatchJob(sctx, client, routes, job)
			backoff = backoffMin // gọi được ERP = mạng lành, reset chờ
			continue
		case errors.Is(err, transport.ErrNoJob):
			// Bình thường: quán chưa có việc in. Hỏi lại NGAY, không chờ.
			backoff = backoffMin
			continue
		case ctx.Err() != nil:
			return false, nil
		}

		// Chỉ tới đây mới là lỗi thật (mất mạng, ERP tắt, token hỏng).
		logf("lỗi: %v — thử lại sau %s", err, backoff)
		if !sleepCtx(ctx, backoff) {
			return false, nil
		}
		backoff = min(backoff*2, backoffMax)
	}
}

/*
drainGroups đóng hàng đợi rồi CHỜ thợ in nốt phần còn lại, trước khi phiên mới dựng lên.

Chờ chứ không cắt: một lệnh in đang dở mà bị bỏ giữa chừng sẽ nằm ở trạng thái `sent`
bên ERP, và hai phút sau `reclaimStuckJobs` đưa nó về hàng đợi — in ra TỜ THỨ HAI cho
cùng một đơn, mà tờ đầu đã nằm trong tay khách nên không ai đối chiếu ra được.

Trần 30 giây vì `Write` vào device node có thể treo tới 15 giây khi kẹt giấy: quá mốc
này thì nhiều khả năng máy in đã treo hẳn, và giữ phiên cũ mãi còn tệ hơn — bản gán mới
không bao giờ có hiệu lực.
*/
func drainGroups(groups map[string]*deviceGroup, wg *sync.WaitGroup) {
	logf("đang chờ các lệnh in dở xong trước khi nạp lại…")
	for _, g := range groups {
		close(g.jobs)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(drainTimeout):
		logf("⚠ quá %s vẫn còn lệnh in chưa xong — nạp lại luôn; lệnh đó có thể bị ERP cho in lại", drainTimeout)
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
	key   string // khoá gom nhóm nội bộ, KHÔNG phải đường dẫn
	label string // để đọc nhật ký
	// deviceKey — chuỗi danh tính gửi lên ERP (`Identity.Key()`). Rỗng khi cấu hình
	// đời cũ chưa có danh tính. Tách khỏi `key`: `key` chỉ để gom hàng đợi trong tiến
	// trình này, còn `deviceKey` là thứ ERP cất và so — hai vai trò khác nhau.
	deviceKey  string
	printer    printer.Printer
	printerIDs []int
	jobs       chan *transport.Job
}

// groupByDevice gom cấu hình thành các nhóm theo thiết bị vật lý.
//
// Trả kèm bảng tra printer_id → nhóm, vì lệnh in từ ERP mang printer_id chứ
// không biết gì về thiết bị.
// identityKeyOf — khoá danh tính để báo lên ERP, rỗng nếu chưa có danh tính.
func identityKeyOf(b config.Binding) string {
	if id := b.Printer.Identity; !id.IsZeroOrNil() {
		return id.Key()
	}
	return ""
}

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
				key:       key,
				label:     describeBinding(b),
				deviceKey: identityKeyOf(b),
				printer:   p,
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
		case job, ok := <-g.jobs:
			// Hàng đợi đóng = phiên đang rút lui để nạp bản gán mới. Kênh có đệm nên
			// các lệnh còn xếp hàng vẫn được giao hết TRƯỚC khi `ok` thành false —
			// đó chính là phép rút lui không đánh rơi lệnh in.
			if !ok {
				return
			}
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
			// `printer.Delivered(err)` = byte đã kịp ra máy in trước khi hỏng. ERP
			// dùng cờ này để KHÔNG tự in lại — không có nó thì mọi lỗi giữa chừng
			// đều đẻ ra tờ thứ hai (sự cố Gác Đờ Rô 2026-08-09).
			if rerr := client.ReportResult(ctx, job.ID, err, printer.Delivered(err)); rerr != nil {
				// In được mà báo không được là tình huống xấu: ERP tưởng hỏng và sẽ
				// cho in lại → ra hai tờ. Ghi rõ để còn lần theo khi có khiếu nại.
				logf("job %d: KHÔNG báo được kết quả về ERP: %v", job.ID, rerr)
			}
		}
	}
}

func reportErr(ctx context.Context, client *transport.Client, jobID int64, cause error) {
	logf("job %d: %v", jobID, cause)
	// Mọi lối vào đây đều hỏng TRƯỚC khi chạm máy in (không cầm máy in đó, hàng đợi
	// tại quầy đầy, payload hỏng) → an toàn cho ERP in lại.
	if rerr := client.ReportResult(ctx, jobID, cause, false); rerr != nil {
		logf("job %d: KHÔNG báo được kết quả về ERP: %v", jobID, rerr)
	}
}

// heartbeatLoop báo máy quầy còn sống + thăm cổng TỪNG THIẾT BỊ.
//
// Thăm theo thiết bị chứ không theo máy in ERP: hai vai trò dùng chung một máy
// mà thăm hai lần thì cú thăm này sẽ va EBUSY với cú in kia, đẻ ra báo động giả
// "máy in mất kết nối" ngay giữa lúc máy in đang chạy tốt. Thăm một lần rồi
// báo cùng một kết quả cho mọi máy in ERP trỏ vào nó.
// Nó cũng là chỗ agent GỬI KIỂM KÊ máy in đang cắm và NHẬN biết ERP đã đổi bản gán
// (047f) — cả hai đi nhờ nhịp có sẵn, không mở thêm vòng lặp nào.
func heartbeatLoop(
	ctx context.Context,
	client *transport.Client,
	groups map[string]*deviceGroup,
	routes map[int]*deviceGroup,
	cfgPath string,
	reload chan<- struct{},
) {
	// -1 chứ không phải 0: máy quầy nào cũng bắt đầu ở rev 0 bên ERP, nên khởi đầu bằng
	// 0 sẽ khiến nhịp đầu tiên coi là "không có gì đổi" và bỏ qua đúng lần đồng bộ đầu.
	// Đó là lần quan trọng nhất — lần một quán cài từ trước bắt kịp bản gán trên ERP.
	lastRev := -1
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
				} else {
					// Chỉ khai khoá khi vừa phân giải + mở được thiết bị. Khai lúc đang
					// hỏng là bảo ERP "tôi vẫn dùng chiếc này" cho một chiếc có thể đã bị
					// rút ra — ERP sẽ chữa bản gán theo một sự thật không còn đúng.
					s.DeviceKey = g.deviceKey
				}
				status = append(status, s)
			}
			rev, err := client.Heartbeat(ctx, status, inventory())
			if err != nil {
				logf("heartbeat lỗi: %v", err)
				continue
			}
			if rev == lastRev {
				continue
			}
			changed, err := syncBindings(ctx, client, cfgPath)
			if err != nil {
				// KHÔNG cập nhật `lastRev`: chưa đồng bộ được thì nhịp sau phải thử lại,
				// nếu không thì một lần mạng chập làm bản gán mới kẹt tới lần đổi kế tiếp.
				logf("tải bản gán từ ERP lỗi: %v", err)
				continue
			}
			lastRev = rev
			if changed {
				// Không chặn: vòng chính có thể đang treo trong một cú long-poll, và nhịp
				// sống tuyệt đối không được đứng lại vì chuyện đó.
				select {
				case reload <- struct{}{}:
				default:
				}
			}
		}
	}
}

/*
inventory — máy in đang cắm ở máy này, để ERP đổ vào ô chọn trên web (047f, CAP-5).

Trả `nil` (⇒ không gửi trường `devices`) khi KHÔNG DÒ ĐƯỢC, và mảng rỗng khi dò được
mà không có máy nào. Phân biệt này đi thẳng xuống tận cột JSONB của ERP: `nil` để ERP
giữ nguyên danh sách cũ, `[]` để ERP xoá. Gộp hai thứ lại thì một lần đọc sysfs hỏng sẽ
xoá sạch ô chọn của người dùng và họ tưởng máy in đã rút.

Bỏ qua thiết bị KHÔNG khai được danh tính: gán vào một thiết bị không phân giải nổi thì
lệnh in nào cũng hỏng, nên bày nó ra ô chọn chỉ là mời người dùng chọn một thứ chắc chắn
sai. Vẫn nói ra ở nhật ký để người lắp biết đường mà tra.
*/
func inventory() []transport.DeviceInfo {
	found, err := printer.Discover()
	if err != nil {
		logf("không dò được máy in để báo lên ERP: %v", err)
		return nil
	}
	out := make([]transport.DeviceInfo, 0, len(found))
	for _, f := range found {
		if f.Identity.IsZero() {
			logf("⚠ %s không khai danh tính — không đưa vào danh sách chọn trên web", f.Path)
			continue
		}
		id := f.Identity
		out = append(out, transport.DeviceInfo{
			DeviceKey: id.Key(),
			Path:      f.Path,
			Model:     id.Model,
			Mfg:       id.Mfg,
			CMD:       id.CMD,
			Serial:    id.Serial,
			IDs:       id.IDs,
			USBPath:   id.USBPath,
		})
	}
	return out
}

/*
syncBindings kéo bản gán ERP đang giữ về rồi ghi xuống tệp cấu hình (047f, CAP-6).

Đọc lại cấu hình TỪ TỆP chứ không sửa bản đang nằm trong bộ nhớ của phiên: phiên đang
chạy dùng bản của nó để phục vụ máy in, và sửa nóng dưới chân nó là cách đẻ ra những lỗi
chỉ xuất hiện lúc quán đông khách. Ghi xong thì phiên mới đọc lại từ tệp — một chiều,
không có trạng thái dùng chung.
*/
func syncBindings(ctx context.Context, client *transport.Client, cfgPath string) (bool, error) {
	rev, remote, err := client.Bindings(ctx)
	if err != nil {
		return false, err
	}
	if remote == nil {
		return false, nil // ERP bản cũ chưa có đường này — chạy theo cấu hình cục bộ
	}

	cur, err := config.Load(cfgPath)
	if err != nil {
		return false, err
	}
	assigns := make([]config.DeviceAssignment, 0, len(remote))
	for _, r := range remote {
		assigns = append(assigns, config.DeviceAssignment{PrinterID: r.PrinterID, DeviceKey: r.DeviceKey})
	}

	changed, warnings := cur.ApplyBindings(assigns)
	for _, w := range warnings {
		logf("⚠ %s", w)
	}
	if !changed {
		return false, nil
	}
	if err := config.Save(cfgPath, cur); err != nil {
		return false, err
	}
	logf("ERP đổi bản gán (bản %d) — đã ghi %s", rev, cfgPath)
	for _, b := range cur.Printers {
		logf("  máy in id %d → %s", b.PrinterID, describeBinding(b))
	}
	return true, nil
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

// Logf — nơi lõi ghi nhật ký. Mặc định in ra stdout kèm giờ (systemd/journalctl
// gom lại); Android thay bằng Logcat trước khi gọi `Run`.
var Logf = func(format string, a ...any) {
	fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func logf(format string, a ...any) { Logf(format, a...) }
