# --- Giai đoạn 1: Build & Fix Dependencies ---
FROM golang:1.22-alpine as builder

# Cài đặt git để tải thư viện
RUN apk add --no-cache git

# Tạo thư mục làm việc
WORKDIR /app

# Copy toàn bộ code vào trước (để go mod tidy quét được code)
COPY . .

# 🔥 MAGIC STEP: Tự động sửa lỗi thư viện
# Lệnh này sẽ tự động thêm các thư viện thiếu và bỏ các thư viện thừa
RUN go mod tidy

# Build ra file chạy (Binary)
RUN CGO_ENABLED=0 GOOS=linux go build -v -o server main.go

# --- Giai đoạn 2: Run ---
FROM gcr.io/distroless/static-debian12
COPY --from=builder /app/server /server
EXPOSE 8080
CMD ["/server"]
