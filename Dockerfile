# Chương trình in ở máy quầy, đóng gói container.
#
# ⚠️ CHỈ CHẠY ĐƯỢC TRÊN LINUX. Container muốn ghi ra máy in USB thì phải được cấp
# thiết bị `/dev/usb/lpN` của máy thật — Docker Desktop trên Windows/macOS chạy trong
# một máy ảo KHÔNG có đường dẫn USB nào để cấp. Đó chính là lý do dự án chọn phương án
# "agent chạy ngoài container" ngay từ đầu (PRD 047); bản Docker này chỉ là lựa chọn
# đóng gói THÊM cho quán đã quen chạy mọi thứ bằng compose.
#
# Máy in LAN thì không cần cái này lẫn agent — backend tự mở TCP 9100.

FROM golang:1.23-alpine AS build
WORKDIR /src
# Không có go.sum vì agent chỉ dùng thư viện chuẩn — không tải gì từ mạng khi build.
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
# Nhãn phiên bản: `docker build --build-arg VERSION=v0.3.0`. Bỏ trống thì `version` in
# ra "dev" — lúc đi chữa máy in ở quán mà không biết đang chạy bản nào thì rất mất công.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/trcf-print-agent ./cmd/agent

# `scratch` cũng đủ (binary tĩnh), nhưng giữ alpine để còn `sh` mà chui vào xem
# `/dev/usb/lp*` khi máy in không ra giấy — lúc đó thứ cần nhất là mở được vỏ ra xem.
FROM alpine:3.20
RUN adduser -D -H trcf
COPY --from=build /out/trcf-print-agent /usr/local/bin/trcf-print-agent

# Cấu hình (token + bản đồ máy in) nằm ngoài ảnh: mount `/etc/trcf-print-agent` vào.
# Mất tệp này là mất token, phải ghép lại bằng mã mới.
VOLUME ["/etc/trcf-print-agent"]

# Chạy dưới root: `/dev/usb/lpN` mặc định là `root:lp 0660`. Muốn hạ quyền thì đổi
# thành `USER trcf` rồi thêm `group_add: ["lp"]` ở compose.
ENTRYPOINT ["/usr/local/bin/trcf-print-agent"]
CMD ["run"]
