package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	// 1. Lấy cổng từ biến môi trường (Cloud Run yêu cầu bắt buộc)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// 2. Định nghĩa Router (Đơn giản trước, sau này sẽ tách file)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Chỉ chấp nhận POST
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		
		// Ping pong để test server sống
		fmt.Fprintf(w, `{"status": "true", "messenger": "System V300 (Go) is Ready!"}`)
	})

	// 3. Khởi động Server
	log.Printf("🚀 Server starting on port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("❌ Failed to start server: %v", err)
	}
}
