# ==========================================
# STAGE 1: Build (Biên dịch code)
# ==========================================
FROM golang:1.22-alpine AS builder

# Cài đặt git (cần thiết để tải dependencies)
RUN apk add --no-cache git

WORKDIR /app

# 🔴 THAY ĐỔI Ở ĐÂY:
# Chỉ copy go.mod trước (vì bạn chưa có go.sum trên git)
COPY go.mod ./

# Tự động tạo go.sum và tải thư viện ngay trong lúc build
RUN go mod tidy
RUN go mod download

# Copy toàn bộ mã nguồn còn lại
COPY . .

# Build file thực thi (Binary)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server .

# ==========================================
# STAGE 2: Run (Môi trường chạy)
# ==========================================
FROM alpine:latest

WORKDIR /root/

# Cài đặt CA Certificates để gọi HTTPS
RUN apk --no-cache add ca-certificates tzdata

# Copy file thực thi từ bước Build
COPY --from=builder /app/server .

# Thiết lập múi giờ Việt Nam
ENV TZ=Asia/Ho_Chi_Minh

# Mở port 8080
EXPOSE 8080

# Chạy server
CMD ["./server"]
