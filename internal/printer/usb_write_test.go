package printer

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDev đóng vai device node: ghi được, đóng được, và đếm được thứ tự hai việc đó.
type fakeDev struct {
	mu       sync.Mutex
	got      []byte
	closed   bool
	closedAt time.Time
	lastWrAt time.Time

	delay    time.Duration // giả lập máy in chậm
	writeErr error
	closeErr error
}

func (d *fakeDev) Write(p []byte) (int, error) {
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.writeErr != nil {
		return 0, d.writeErr
	}
	d.got = append(d.got, p...)
	d.lastWrAt = time.Now()
	return len(p), nil
}

func (d *fakeDev) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	d.closedAt = time.Now()
	return d.closeErr
}

func (d *fakeDev) snapshot() (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.got), d.closed
}

func TestWriteUSB_GhiTronRoiMoiDong(t *testing.T) {
	// Bug 2026-08-06: đóng thiết bị trước khi dữ liệu ra hết dây USB làm bill cụt
	// đuôi mà vẫn báo thành công. Đuôi bill chính là lệnh cắt giấy.
	d := &fakeDev{}
	bill := []byte("TRCF\nHOA DON BAN HANG\nTONG CONG: 70.000\n\x1bd\x04\x1dVB\x00")

	if err := writeUSB(d, bill, "/dev/usb/lp0"); err != nil {
		t.Fatalf("không mong có lỗi: %v", err)
	}

	got, closed := d.snapshot()
	if got != string(bill) {
		t.Errorf("thiết bị nhận thiếu byte:\n có: %q\n cần: %q", got, string(bill))
	}
	if !closed {
		t.Error("phải đóng thiết bị sau khi ghi xong")
	}
	if !d.closedAt.After(d.lastWrAt) && !d.closedAt.Equal(d.lastWrAt) {
		t.Error("đóng thiết bị TRƯỚC khi ghi xong — đúng cái bug đang phòng")
	}
}

func TestWriteUSB_LoiGhiThiBaoLoiVaVanDong(t *testing.T) {
	d := &fakeDev{writeErr: errors.New("kẹt giấy")}
	err := writeUSB(d, []byte("abc"), "/dev/usb/lp0")
	if err == nil {
		t.Fatal("ghi hỏng mà báo thành công — đây là kiểu hỏng im lặng phải chặn")
	}
	if _, closed := d.snapshot(); !closed {
		t.Error("ghi hỏng vẫn phải đóng thiết bị, không được rò fd")
	}
}

func TestWriteUSB_LoiDongCungPhaiBao(t *testing.T) {
	// usblp đẩy nốt phần cuối lúc close → close hỏng nghĩa là đuôi bill có thể
	// chưa ra giấy, không được im lặng coi như in xong.
	d := &fakeDev{closeErr: errors.New("thiết bị biến mất")}
	err := writeUSB(d, []byte("abc"), "/dev/usb/lp0")
	if err == nil || !strings.Contains(err.Error(), "đóng") {
		t.Fatalf("cần báo lỗi lúc đóng, nhận: %v", err)
	}
	// Nhưng KHÔNG được để backend in lại: tới được nhánh này nghĩa là toàn bộ buffer
	// đã qua cổng USB (fd chặn), giấy đã ra gần đủ — thiếu cùng lắm là phần đuôi. In
	// lại là ra nguyên một tờ thứ hai trên cùng máy in.
	if !Delivered(err) {
		t.Error("lỗi lúc ĐÓNG phải gắn Delivered=true — ghi đã xong, chỉ đóng hỏng")
	}
}

// shortenWriteLimits rút cả hai mốc để test chạy trong mili-giây. Treo thật 15+45
// giây thì không ai chạy test nữa, mà đây đúng là nhánh cần có test nhất.
func shortenWriteLimits(t *testing.T, timeout, grace time.Duration) {
	t.Helper()
	oldT, oldG := usbWriteTimeout, usbWriteGrace
	usbWriteTimeout, usbWriteGrace = timeout, grace
	t.Cleanup(func() { usbWriteTimeout, usbWriteGrace = oldT, oldG })
}

func TestWriteUSB_MayInTreoQuaCaHanChoThiBaoLoiChuKhongDungIm(t *testing.T) {
	// Đây là lý do trước đây có SetWriteDeadline. Bỏ deadline rồi thì trần thời
	// gian phải do chỗ này giữ, nếu không máy in kẹt giấy sẽ treo agent vĩnh viễn.
	shortenWriteLimits(t, 30*time.Millisecond, 60*time.Millisecond)

	d := &fakeDev{delay: 2 * time.Second}
	start := time.Now()
	err := writeUSB(d, []byte("abc"), "/dev/usb/lp0")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("máy in treo mà báo in xong")
	}
	if !strings.Contains(err.Error(), "không nhận hết dữ liệu") {
		t.Errorf("câu lỗi phải chỉ ra việc cần làm, nhận: %v", err)
	}
	// Treo qua CẢ hai mốc = đã đẩy đi kha khá byte, giấy có thể đã ra. Backend phải
	// biết để KHÔNG tự in lại — thiếu cờ này là tái diễn sự cố in lặp 2026-08-09.
	if !Delivered(err) {
		t.Error("treo giữa chừng phải gắn Delivered=true, nếu không backend sẽ tự in lại tờ thứ hai")
	}
	if elapsed > time.Second {
		t.Errorf("phải bỏ cuộc sau ~%s, thực tế chờ %s", usbWriteTimeout+usbWriteGrace, elapsed)
	}
	// Quá hạn thì KHÔNG được đóng: fd thô bị đóng trong lúc luồng khác còn kẹt
	// giữa write() sẽ được cấp lại cho tệp mới mở, và byte máy in đổ nhầm vào đó.
	if _, closed := d.snapshot(); closed {
		t.Error("quá hạn mà vẫn đóng thiết bị — mở đường cho lỗi cấp lại số hiệu fd")
	}
}

// Đây là test giữ cửa cho sự cố Gác Đờ Rô 2026-08-09 (17/687 bill in lặp).
//
// Máy in khựng quá mốc mềm rồi in xong trong hạn chờ thêm KHÔNG phải lỗi. Bản
// trước báo hỏng ngay ở mốc mềm trong khi lệnh ghi vẫn đang chạy, backend gửi lại
// trọn payload → tờ thứ hai chui ra trong lúc tờ đầu chưa in xong.
func TestWriteUSB_KhungRoiXongTrongHanChoThemThiKhongPhaiLoi(t *testing.T) {
	shortenWriteLimits(t, 30*time.Millisecond, 3*time.Second)

	bill := []byte("TRCF\nHOA DON\n\x1dVB\x00")
	d := &fakeDev{delay: 200 * time.Millisecond} // khựng, nhưng vẫn xong

	if err := writeUSB(d, bill, "/dev/usb/lp0"); err != nil {
		t.Fatalf("máy in chỉ chậm chứ in đủ — báo lỗi ở đây là backend in lại tờ thứ hai: %v", err)
	}
	got, closed := d.snapshot()
	if got != string(bill) {
		t.Errorf("thiết bị nhận thiếu byte:\n có: %q\n cần: %q", got, string(bill))
	}
	if !closed {
		t.Error("ghi xong (dù chậm) vẫn phải đóng thiết bị")
	}
}
