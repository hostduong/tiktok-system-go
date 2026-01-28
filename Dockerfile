# ==========================================
# STAGE 1: Build (Biên dịch code)
# ==========================================
FROM golang:1.22-alpine AS builder

# Cài đặt git
RUN apk add --no-cache git

WORKDIR /app

# 🔴 THAY ĐỔI QUAN TRỌNG:
# Copy TOÀN BỘ mã nguồn vào trước (bao gồm go.mod, main.go, folder handlers...)
COPY . .

# Sau khi có code, chạy lệnh này để nó quét các file .go và tự động tải thư viện thiếu
RUN go mod tidy

# Tải dependencies về
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
