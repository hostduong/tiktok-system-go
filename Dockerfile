# ==========================================
# STAGE 1: Build (Biên dịch code)
# ==========================================
FROM golang:alpine AS builder

# Cài đặt git
RUN apk add --no-cache git

WORKDIR /app

# Copy toàn bộ mã nguồn
COPY . .

# 🔥 UPDATE: Tải thư viện Firebase v4 (Bản mới nhất hỗ trợ Asia)
RUN go get firebase.google.com/go/v4

# Dọn dẹp và tải các thư viện khác
RUN go mod tidy
RUN go mod download

# Build file thực thi
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server .

# ==========================================
# STAGE 2: Run (Môi trường chạy)
# ==========================================
FROM alpine:latest

WORKDIR /root/

# Cài đặt chứng chỉ
RUN apk --no-cache add ca-certificates tzdata

# Copy file thực thi
COPY --from=builder /app/server .

# Thiết lập múi giờ
ENV TZ=Asia/Ho_Chi_Minh

# Mở port
EXPOSE 8080

# Chạy server
CMD ["./server"]
