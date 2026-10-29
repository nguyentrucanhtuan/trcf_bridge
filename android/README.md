# TRCF POS — app Android (máy iMin I24D02)

App **cầu nối in** cho máy POS Android: in bill và tem ra máy in cắm USB, chạy nền cả
ngày, tự bật lại sau khi khởi động máy. Màn bán hàng mở bằng trình duyệt như mọi máy khác.

> Bản 0.1.0 có nhúng màn bán hàng (`/pos-app`) và màn khách trên màn phụ; **đã gỡ ở 0.1.1**.
> ERP trên cloud tách domain backend với frontend nên trang nhúng mở ra trắng, và nó còn
> tự bật lên che màn hình mỗi lần cắm máy in hay khởi động máy. Muốn có lại thì xem git
> history của `MainActivity.kt` + `CustomerDisplay.kt`.

Phần in là **cùng một lõi Go** với `trcf-bridge` trên Linux (`../internal/core`), đóng gói
bằng gomobile. App chỉ thêm đường USB của Android (`UsbPrinterHost.kt`) và giao diện.

## Lắp đặt ở quán (5 phút)

1. Cắm máy in vào cổng USB của máy POS, bật nguồn máy in.
2. Cài `trcf-pos.apk` (chép qua USB/Zalo rồi mở file, cho phép "cài từ nguồn này").
3. Android hỏi *"Mở TRCF POS khi kết nối thiết bị này?"* → tích **Luôn dùng** → OK.
   (Tích ô này thì sau khởi động lại không phải cho phép lại.)
4. Mở app → màn thiết lập:
   - **Địa chỉ ERP**: địa chỉ có `/bridge/*` vào backend — vd `https://erp.quan.vn`
     (cloud) hoặc `http://192.168.1.50` (máy chủ trong quán)
   - **Mã ghép**: trên ERP vào **Máy in** → chọn máy in → **Lấy mã ghép**
   - Chọn máy in → **In thử** (giấy ra và đứt rời là đúng) → **Ghép & bắt đầu**

Xong. Tắt máy bật lại thì việc in tự chạy, không cần mở app. Nên vào **Cài đặt → Ứng
dụng → TRCF POS → Pin → Không hạn chế** để chế độ tiết kiệm pin không chặn dịch vụ in.

**Máy in bếp / máy in thứ hai:** chạm thông báo *"TRCF — đang sẵn sàng in"* → màn thiết
lập → lấy mã của máy in kia → chọn máy → **Ghép & bắt đầu**. Vẫn một máy quầy bên ERP.

**Có sự cố:** chạm thông báo *"TRCF — đang sẵn sàng in"* để xem nhật ký. Mọi lỗi in cũng
hiện ở màn bán hàng (băng đỏ "lệnh in chưa ra giấy") và màn Máy in trên ERP.

## Giới hạn đã biết

- Chỉ máy in **rời cắm USB** (lớp máy in chuẩn). Máy in liền thân iMin, Bluetooth chưa hỗ trợ.
  Máy in LAN thì không cần app: ERP in thẳng qua mạng.
- Hai máy in **cùng model và cùng không có số sê-ri** trên một máy POS: app không phân biệt
  được (Android không cho biết vị trí cổng USB) nên **từ chối in** và báo lỗi, chứ không in nhầm
  máy. Gặp ca này thì thay một máy khác model.

## Build

```bash
./build-core.sh                 # lõi Go → app/libs/trcfcore.aar (cần gomobile, NDK, JDK)
./gradlew assembleRelease       # → app/build/outputs/apk/release/app-release.apk
```

Chạy lại `build-core.sh` mỗi khi code Go đổi. APK release ký bằng khoá debug để cài
được ngay; đổi khoá phát hành khi có kênh cập nhật chính thức.
