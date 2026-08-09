package printer

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// chunkWriter mô phỏng device node của máy in: mỗi lần chỉ nuốt tối đa chunk
// byte, đúng như usblp khi bộ đệm máy in gần đầy.
type chunkWriter struct {
	buf   bytes.Buffer
	chunk int
	calls int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	w.calls++
	n := len(p)
	if n > w.chunk {
		n = w.chunk
	}
	w.buf.Write(p[:n])
	return n, nil
}

// Đây là bài test quan trọng nhất của agent: máy in nhận nhỏ giọt thì buffer
// vẫn phải tới nơi ĐỦ, không mất byte cuối (nơi có lệnh cắt giấy).
func TestWriteAllHandlesPartialWrites(t *testing.T) {
	// 100 byte kết thúc bằng lệnh cắt giấy — mô phỏng một bill thật.
	payload := append(bytes.Repeat([]byte{'X'}, 96), 0x1D, 0x56, 0x42, 0x00)

	w := &chunkWriter{chunk: 7}
	if err := writeAll(w, payload, "/dev/usb/lp0"); err != nil {
		t.Fatalf("writeAll: %v", err)
	}

	if got := w.buf.Bytes(); !bytes.Equal(got, payload) {
		t.Fatalf("ghi thiếu: nhận %d byte, muốn %d", len(got), len(payload))
	}
	if w.calls < 2 {
		t.Fatalf("test không thực sự chia nhỏ (calls=%d)", w.calls)
	}
	if !bytes.HasSuffix(w.buf.Bytes(), []byte{0x1D, 0x56, 0x42, 0x00}) {
		t.Fatal("mất lệnh cắt giấy ở cuối — bill sẽ không đứt")
	}
}

// stuckWriter: nhận 0 byte và KHÔNG báo lỗi — máy in hết giấy hay bị treo.
type stuckWriter struct{ calls int }

func (w *stuckWriter) Write(p []byte) (int, error) { w.calls++; return 0, nil }

// Không được quay vô hạn: agent treo im lặng còn tệ hơn báo lỗi, vì ERP sẽ
// tưởng job vẫn đang chạy và không ai biết bill chưa ra (PRD §7).
func TestWriteAllStopsWhenPrinterStalls(t *testing.T) {
	w := &stuckWriter{}

	err := writeAll(w, []byte("abc"), "/dev/usb/lp0")
	if err == nil {
		t.Fatal("muốn lỗi khi máy in không nhận thêm byte")
	}
	if w.calls > 3 {
		t.Errorf("gọi Write %d lần — có dấu hiệu quay vòng thay vì dừng sớm", w.calls)
	}
	if !strings.Contains(err.Error(), "hết giấy") {
		t.Errorf("thông báo không gợi ý nguyên nhân thường gặp: %v", err)
	}
}

type errWriter struct{ err error }

func (w *errWriter) Write(p []byte) (int, error) { return 0, w.err }

// Lỗi phải kèm đã-gửi-bao-nhiêu: khi đi hỗ trợ, "dừng ở 0/240 byte" và
// "dừng ở 238/240 byte" dẫn tới hai chẩn đoán khác hẳn nhau.
func TestWriteAllErrorReportsProgress(t *testing.T) {
	sentinel := errors.New("input/output error")

	err := writeAll(&errWriter{err: sentinel}, make([]byte, 240), "/dev/usb/lp0")
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("mất lỗi gốc: %v", err)
	}
	if !strings.Contains(err.Error(), "0/240") {
		t.Errorf("thiếu tiến độ trong thông báo: %v", err)
	}
	if !strings.Contains(err.Error(), "/dev/usb/lp0") {
		t.Errorf("thiếu tên máy in trong thông báo: %v", err)
	}
}

// partialThenErrWriter: nuốt được một ít rồi mới hỏng — đúng hình dạng của máy in
// hết giấy giữa tờ bill.
type partialThenErrWriter struct {
	ok  int
	err error
}

func (w *partialThenErrWriter) Write(p []byte) (int, error) {
	if w.ok > 0 {
		n := min(w.ok, len(p))
		w.ok -= n
		return n, nil
	}
	return 0, w.err
}

// Ranh giới quyết định TOÀN BỘ chính sách thử lại của backend (sự cố Gác Đờ Rô
// 2026-08-09): hỏng ở byte thứ 0 thì giấy chưa hề chạy → in lại được; hỏng sau khi
// đã đẩy đi byte nào đó thì nửa tờ bill đã nằm trên khay → in lại là ra hai tờ.
func TestWriteAllPhanLoaiDaChamGiayChua(t *testing.T) {
	boom := errors.New("input/output error")

	t.Run("hỏng ngay byte đầu → chưa chạm giấy", func(t *testing.T) {
		err := writeAll(&errWriter{err: boom}, make([]byte, 240), "/dev/usb/lp0")
		if Delivered(err) {
			t.Error("chưa gửi byte nào mà báo đã chạm giấy → backend sẽ KHÔNG in lại, mất bill thật")
		}
	})

	t.Run("hỏng giữa chừng → đã chạm giấy", func(t *testing.T) {
		err := writeAll(&partialThenErrWriter{ok: 100, err: boom}, make([]byte, 240), "/dev/usb/lp0")
		if err == nil {
			t.Fatal("muốn lỗi")
		}
		if !Delivered(err) {
			t.Error("đã gửi 100/240 byte mà báo chưa chạm giấy → backend tự in lại tờ thứ hai")
		}
		if !strings.Contains(err.Error(), "100/240") {
			t.Errorf("thiếu tiến độ trong thông báo: %v", err)
		}
	})

	t.Run("máy in ngừng nhận giữa chừng → đã chạm giấy", func(t *testing.T) {
		err := writeAll(&partialThenErrWriter{ok: 50}, make([]byte, 240), "/dev/usb/lp0")
		if err == nil {
			t.Fatal("muốn lỗi")
		}
		if !Delivered(err) {
			t.Error("đã ra 50 byte rồi mới kẹt → không được tự in lại")
		}
	})
}

// Lỗi KHÔNG phải WriteError (mở thiết bị, quay số TCP) đều xảy ra trước khi có byte
// nào đi. Mặc định `false` để backend cứ thử lại — thử lại một lệnh chưa chạm giấy
// thì không tốn gì.
func TestDeliveredMacDinhLaFalse(t *testing.T) {
	if Delivered(nil) {
		t.Error("nil mà báo đã chạm giấy")
	}
	if Delivered(errors.New("không thấy máy in tại /dev/usb/lp2")) {
		t.Error("lỗi mở thiết bị phải coi là chưa chạm giấy để còn in lại được")
	}
}

// Buffer rỗng = phép thăm cổng của heartbeat (mở được device là máy in sống),
// phải thành công mà không tốn giấy.
func TestWriteAllEmptyIsNoOp(t *testing.T) {
	w := &chunkWriter{chunk: 8}

	if err := writeAll(w, nil, "/dev/usb/lp0"); err != nil {
		t.Fatalf("writeAll(nil): %v", err)
	}
	if w.calls != 0 {
		t.Errorf("buffer rỗng vẫn gọi Write %d lần", w.calls)
	}
}
