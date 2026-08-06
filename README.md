# TRCF Bridge

Chương trình nhỏ chạy nền trên **máy tại quầy**, ngoài Docker. Là **cầu nối giữa TRCF ERP và
các thiết bị vật lý cắm ở quầy**.

Hôm nay nó làm đúng một việc: nhận byte ESC/POS từ ERP rồi ghi ra máy in nhiệt (USB hoặc LAN),
cắt giấy, mở ngăn kéo tiền. Về sau những thiết bị chỉ với tới được từ trong quán — máy chấm công,
màn hình khách trên máy tính bảng, cân điện tử — cũng đi qua đây, nhất là khi ERP chuyển lên cloud
và không còn nằm cùng mạng LAN với thiết bị.

> **Đổi tên (2026-08-06):** trước đây gọi là `trcf_print_agent`, binary `trcf-print-agent`,
> đường gọi backend `/print-agent/*`. Đổi thành `trcf_bridge` vì tên cũ mô tả *một việc*,
> trong khi vai của nó là *một chỗ đứng* — thêm loại thiết bị mới thì tên cũ sẽ sai.
> Đường cũ **đã gỡ hẳn, không giữ lớp tương thích nào**: lúc đổi tên chưa quán nào ghép nối
> (tất cả đang in qua LAN) nên không có bản cài ngoài kia để đỡ. Script cài vẫn dọn giúp
> dấu vết bản cũ nếu máy từng cài mà bỏ đó.

PRD: [`prd-46-print-agent-2026-08-02`](../_bmad-output/planning-artifacts/prds/prd-46-print-agent-2026-08-02/prd.md) · Nền nghiên cứu: [báo cáo kỹ thuật](../_bmad-output/planning-artifacts/research/technical-in-hoa-don-may-in-nhiet-usb-docker-research-2026-08-02.md)

**Trạng thái:** Linux xong (`list` / `test` / `pair` / `run`), đã nối được với ERP (backend 047-B) và một máy tính cầm được nhiều máy in (047-D). Windows là đợt sau. `list` và `test` chạy được **không cần ERP**, để kiểm tra máy in thật ngay lúc lắp.

## Nguyên tắc: cầu nối phải ngu

Nó **không** render bill, **không** biết nghiệp vụ, **không** chạm DB, **không** nhận kết nối vào, **không** có UI.

Byte ESC/POS do backend dựng. Agent chỉ nhận buffer → ghi ra cổng → báo kết quả.

Agent nằm ở máy khách hàng, mỗi lần cập nhật là một lần phải đi từng quán. Agent càng ngu thì càng không phải cập nhật — đổi mẫu bill, thêm logo, đổi cách chia phiếu bếp đều chỉ là deploy backend.

Ngoại lệ duy nhất là gói [`internal/escpos`](internal/escpos/escpos.go): đúng lượng lệnh tối thiểu để **tự in thử được khi chưa có backend**.

## Dùng

### 1. Xem máy in nào đang cắm

```bash
trcf-bridge list
```

```
Thấy 2 máy in:

  [1] Xprinter XP-80C (/dev/usb/lp0)
      mã thiết bị: 0483:5743
  [2] Xprinter XP-58IIH (/dev/usb/lp1)
      mã thiết bị: 0416:5011

⚠ Có nhiều hơn một máy in: tên /dev/usb/lpN có thể đổi chỗ cho nhau
  sau khi khởi động lại máy. Nên cố định tên bằng udev rule.
```

Không thấy gì thì nó tự chỉ ra cách chẩn đoán (`lsmod | grep usblp`, `lsusb`).

### 2. In thử — không cần ERP

```bash
trcf-bridge test --device /dev/usb/lp0 --drawer
```

Máy chỉ có **một** máy in thì bỏ luôn `--device`, agent tự chọn. Máy in LAN thì `--net 192.168.1.60:9100`.

Tờ giấy in ra trả lời **bốn** câu trong một lần nhìn:

| Nhìn gì | Kết luận |
|---|---|
| Có chữ ra không | máy in nhận byte |
| Giấy có **đứt rời** không | lệnh cắt chạy đúng |
| Dòng *"Tiếng Việt CÓ dấu"* ra chữ hay ra rác | máy in có bảng mã tiếng Việt không → nếu rác thì backend phải in chữ dưới dạng **ảnh** |
| Hàng `...<-32` / `...<-48` có xuống dòng không | khổ giấy 58mm hay 80mm |

Két không bật thì đổi chân RJ11: `--drawer-pin 1` (mặc định 0 = chân 2, đa số két ở VN dùng chân này).

### 3. Ghép với ERP

```bash
sudo trcf-bridge pair --server http://192.168.1.50 --code 123456
```

Thiếu cờ nào nó hỏi ngay tại chỗ. Nó **in thử trước khi ghép** — máy in chưa in được thì dừng luôn, không tạo bản ghi rác bên ERP.

Xong thì tự lưu `/etc/trcf-bridge/config.json` (quyền 600 vì chứa token) và cài dịch vụ nền.

**Máy tính cắm nhiều máy in** thì chạy `pair` thêm lần nữa với mã của máy in kia:

```bash
sudo trcf-bridge pair --server http://192.168.1.50 --code 654321 --device /dev/usb/lp1
sudo systemctl restart trcf-bridge
```

Lần thứ hai **không** đẻ ra token mới: agent gửi kèm token đang có, ERP gắn thêm máy in vào chính máy quầy này. Vẫn **một** tiến trình, **một** dịch vụ nền, **một** tệp cấu hình cho tất cả máy in.

### 4. Chạy nền

`pair` đã cài systemd unit rồi. Xem nhật ký:

```bash
journalctl -u trcf-bridge -f
```

## Cố định tên máy in (udev)

`/dev/usb/lp0` **không ổn định**: rút cắm lại, hoặc bật máy in theo thứ tự khác, là `lp0` và `lp1` đổi chỗ cho nhau — bill của khách chạy ra máy in bếp.

Bắt buộc làm khi quán có **từ hai máy in USB trở lên**. Lấy mã thiết bị từ `list`, rồi:

```bash
sudo tee /etc/udev/rules.d/99-trcf-printer.rules > /dev/null <<'EOF'
# Máy in bill ở quầy — thay idVendor/idProduct bằng mã lấy từ `list`
SUBSYSTEM=="usbmisc", SUBSYSTEMS=="usb", ATTRS{idVendor}=="0483", ATTRS{idProduct}=="5743", SYMLINK+="trcf-printer-bill", MODE="0660", GROUP="lp"
# Máy in phiếu bếp
SUBSYSTEM=="usbmisc", SUBSYSTEMS=="usb", ATTRS{idVendor}=="0416", ATTRS{idProduct}=="5011", SYMLINK+="trcf-printer-kot", MODE="0660", GROUP="lp"
EOF

sudo udevadm control --reload-rules && sudo udevadm trigger
```

Rồi ghép bằng tên cố định:

```bash
sudo trcf-bridge pair --server http://192.168.1.50 --code 123456 \
     --device /dev/trcf-printer-bill
```

> Hai máy in **cùng model** thì `idVendor:idProduct` giống hệt nhau — lúc đó phải phân biệt bằng `ATTRS{serial}` (nếu máy in có số sê-ri) hoặc bằng **cổng USB vật lý** (`KERNELS=="1-3"`, dán nhãn lên cổng để đừng ai cắm đổi).

## Bố cục

```
cmd/agent/           4 lệnh: list · test · pair · run
internal/printer/    interface Write([]byte) — 3 cài đặt: usb_linux · net · stub
internal/escpos/     trang in thử + cắt giấy + đá két (KHÔNG dựng bill)
internal/config/     config.json (server_url, token, printers[], drawer_pin)
internal/transport/  long-poll + báo kết quả + heartbeat
internal/pair/       đổi mã 6 số lấy token
internal/service/    cài systemd unit
```

Toàn bộ khác biệt USB/LAN/Windows/Linux gói trong **một interface**:

```go
type Printer interface{ Write(b []byte) error }
```

## Build

```bash
go test ./...
GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=v0.1.0" \
  -o dist/trcf-bridge-linux-amd64 ./cmd/agent
```

Ra một file ~6 MB, không cần runtime, không cần cài kèm gì.

## Giao thức với ERP

Agent **gọi ra**, ERP không gọi vào → máy quầy không mở cổng, không cần IP tĩnh, xuyên NAT. Khi backend dời lên cloud thì không phải đổi gì ở quán.

```
POST /bridge/pair                  {code, hostname, os, printer_desc, version}
     [Authorization: Bearer <token cũ>]  → {token, printer_id, printer_name}
     ↑ gửi kèm token đang có = GẮN THÊM máy in vào máy quầy này, trả lại token cũ
GET  /bridge/jobs/next?wait=25     → 200 {job_id, printer_id, format, data(base64)} | 204
POST /bridge/jobs/{id}/result      {ok, error?}
POST /bridge/heartbeat             {version, printers:[{printer_id, port_ok, last_err}]}
                                          mỗi 30s
```

Cần thêm route `/bridge/*` → backend trong Caddyfile của [`trcf_deploy`](../trcf_deploy/Caddyfile).

## Khi nào KHÔNG cần agent

**Máy in có cổng LAN thì không cần.** Cắm dây mạng, đặt IP tĩnh, backend in thẳng cổng 9100 — quán cài 0 thứ.

Agent chỉ dành cho máy in mà backend không với tới: máy thuần USB, máy khác VLAN, hoặc khi backend chạy trên cloud (lúc đó là tất cả).

**Việc đầu tiên khi khảo sát quán: lật máy in lên đếm cổng.**
