# --- Giai đoạn 1: Build ---
FROM golang:1.24-alpine as builder

# Cài git và tzdata (quan trọng cho giờ VN)
RUN apk add --no-cache git tzdata

WORKDIR /app
COPY . .

# Reset module để tránh lỗi cache cũ
RUN rm -f go.sum go.mod
RUN go mod init tiktok-server
RUN go mod tidy

# Build binary nhỏ gọn
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server main.go

# --- Giai đoạn 2: Run ---
FROM gcr.io/distroless/static-debian12

# Copy múi giờ từ builder sang (Sửa lỗi panic time)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
ENV TZ=Asia/Ho_Chi_Minh

COPY --from=builder /app/server /server
EXPOSE 8080
CMD ["/server"]
