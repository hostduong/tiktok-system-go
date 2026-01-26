package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

    // Import để đảm bảo code biên dịch không lỗi (dù chưa dùng tới)
	_ "tiktok-server/internal/cache"
	_ "tiktok-server/internal/models"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		
        // Trả về JSON đúng chuẩn Node.js
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status": "true", "messenger": "Hệ thống TikTok Go V300 đã sẵn sàng!"}`)
	})

	log.Printf("🚀 Server TikTok Go đang chạy tại port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("❌ Không thể khởi động server: %v", err)
	}
}
