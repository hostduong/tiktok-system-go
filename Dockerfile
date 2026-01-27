# --- Giai đoạn 1: Build & Fix Dependencies ---
# 🔥 SỬA Ở ĐÂY: Đổi từ 1.22 thành 1.24
FROM golang:1.24-alpine as builder

# Cài git
RUN apk add --no-cache git

WORKDIR /app

# Copy toàn bộ code
COPY . .

# 🔥 MAGIC FIX:
# Xóa file cũ và khởi tạo lại module
RUN rm -f go.sum
RUN rm -f go.mod
RUN go mod init tiktok-server

# Tự động tìm và tải thư viện (Lúc này nó sẽ dùng Go 1.24 nên sẽ tải được thư viện Google mới)
RUN go mod tidy

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -v -o server main.go

# --- Giai đoạn 2: Run ---
FROM gcr.io/distroless/static-debian12
COPY --from=builder /app/server /server
EXPOSE 8080
CMD ["/server"]
