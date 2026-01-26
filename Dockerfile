# --- Giai đoạn 1: Build & Fix Dependencies ---
FROM golang:1.22-alpine as builder

# Cài git
RUN apk add --no-cache git

WORKDIR /app

# Copy toàn bộ code
COPY . .

# 🔥 MAGIC FIX:
# 1. Xóa file go.sum cũ (nếu có) để tránh xung đột checksum
# 2. Xóa file go.mod cũ và tạo mới lại (để chắc chắn không còn rác)
RUN rm -f go.sum
RUN rm -f go.mod
RUN go mod init tiktok-server

# 3. Tự động tìm và tải thư viện dựa trên code thực tế (import)
RUN go mod tidy

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -v -o server main.go

# --- Giai đoạn 2: Run ---
FROM gcr.io/distroless/static-debian12
COPY --from=builder /app/server /server
EXPOSE 8080
CMD ["/server"]
