# ==========================================
# STAGE 1: Build (Biên dịch code)
# ==========================================
FROM golang:1.22-alpine AS builder

# Cài đặt git (cần thiết để tải dependencies)
RUN apk add --no-cache git

WORKDIR /app

# Copy file quản lý thư viện trước để tận dụng Docker cache
COPY go.mod go.sum ./
RUN go mod download

# Copy toàn bộ mã nguồn
COPY . .

# Build file thực thi (Binary)
# CGO_ENABLED=0: Tắt CGO để tạo static binary (chạy được mọi nơi)
# -ldflags="-w -s": Loại bỏ thông tin debug để giảm dung lượng file
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server .

# ==========================================
# STAGE 2: Run (Môi trường chạy)
# ==========================================
FROM alpine:latest

WORKDIR /root/

# Cài đặt CA Certificates để gọi HTTPS (Google API, Firebase) không bị lỗi SSL
RUN apk --no-cache add ca-certificates tzdata

# Copy file thực thi từ bước Build
COPY --from=builder /app/server .

# Thiết lập múi giờ Việt Nam (Tùy chọn, tốt cho log)
ENV TZ=Asia/Ho_Chi_Minh

# Mở port 8080 (Cloud Run mặc định)
EXPOSE 8080

# Chạy server
CMD ["./server"]
