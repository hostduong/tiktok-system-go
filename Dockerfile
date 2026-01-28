# ==========================================
# STAGE 1: Build (Biên dịch code)
# ==========================================
FROM golang:1.23 AS builder

# Thiết lập thư mục làm việc
WORKDIR /app

# Copy toàn bộ mã nguồn
COPY . .

# Xóa file cũ để tránh xung đột
RUN rm -f go.mod go.sum

# Khởi tạo module mới
RUN go mod init github.com/hostduong/tiktok-system-go

# Tải thư viện Firebase V4
RUN go get firebase.google.com/go/v4@latest

# Tải các thư viện khác
RUN go mod tidy
RUN go mod download

# Build file thực thi (Static Binary)
# CGO_ENABLED=0 để đảm bảo chạy được trên mọi Linux
RUN CGO_ENABLED=0 GOOS=linux go build -v -o server .

# ==========================================
# STAGE 2: Run (Môi trường chạy - Dùng Debian Slim cho ổn định)
# ==========================================
FROM debian:bookworm-slim

# Cài đặt chứng chỉ bảo mật và múi giờ (Quan trọng)
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

# Thiết lập múi giờ Việt Nam
ENV TZ=Asia/Ho_Chi_Minh

# Thư mục làm việc
WORKDIR /root/

# Copy file thực thi từ builder
COPY --from=builder /app/server .

# Mở port
EXPOSE 8080

# Chạy server
CMD ["./server"]
