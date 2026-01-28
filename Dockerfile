# ==========================================
# STAGE 1: Build (Biên dịch code)
# ==========================================
# Dùng bản alpine mới nhất (thường là Go 1.23+) để tối ưu tương thích
FROM golang:alpine AS builder

# Cài đặt git
RUN apk add --no-cache git

WORKDIR /app

# Copy TOÀN BỘ mã nguồn vào trước
COPY . .

# 🔥 FIX LỖI VERSION:
# Ép xuống phiên bản Firestore ổn định tương thích với Go hiện tại
# (Tránh bản v1.21.0 yêu cầu Go 1.24 gây lỗi)
RUN go get cloud.google.com/go/firestore@v1.19.0

# Sau đó mới chạy tidy để dọn dẹp và tải các thư viện khác
RUN go mod tidy

# Tải dependencies
RUN go mod download

# Build file thực thi
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server .

# ==========================================
# STAGE 2: Run (Môi trường chạy)
# ==========================================
FROM alpine:latest

WORKDIR /root/

# Cài đặt chứng chỉ bảo mật và múi giờ
RUN apk --no-cache add ca-certificates tzdata

# Copy file thực thi từ builder
COPY --from=builder /app/server .

# Thiết lập múi giờ Việt Nam
ENV TZ=Asia/Ho_Chi_Minh

# Mở port
EXPOSE 8080

# Chạy server
CMD ["./server"]
