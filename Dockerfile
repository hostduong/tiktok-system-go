# --- Giai đoạn 1: Build (Biên dịch) ---
FROM golang:1.22-alpine as builder

# Tạo thư mục làm việc
WORKDIR /app

# Copy file quản lý thư viện trước để tận dụng cache
COPY go.mod ./
# COPY go.sum ./  <-- Bỏ comment dòng này khi bạn đã chạy 'go mod tidy' lần đầu

# Tải thư viện (Nếu có)
RUN go mod download

# Copy toàn bộ code vào
COPY . .

# Build ra file chạy (Binary) tên là 'server'
# CGO_ENABLED=0 giúp tạo file static binary chạy được trên mọi Linux
RUN CGO_ENABLED=0 GOOS=linux go build -v -o server main.go

# --- Giai đoạn 2: Run (Chạy thật) ---
# Dùng ảnh 'distroless' cực nhẹ, bảo mật cao (không có shell, không cài được virus)
FROM gcr.io/distroless/static-debian12

# Copy file binary từ giai đoạn 1 sang
COPY --from=builder /app/server /server

# Mở cổng 8080 (Thông báo thôi, Cloud Run tự quản lý)
EXPOSE 8080

# Chạy server
CMD ["/server"]
