package main

import (
	"log"
	"net/http"
	"os"

	"tiktok-server/internal/auth"
	"tiktok-server/internal/handlers"
	"tiktok-server/internal/sheets"
)

func main() {
	// 1. Khởi tạo Auth (Firebase)
	authSvc, err := auth.NewAuthenticator()
	if err != nil {
		log.Fatalf("❌ Lỗi khởi tạo Firebase: %v", err)
	}

	// 2. Khởi tạo Google Sheets Service
	sheetSvc, err := sheets.NewService()
	if err != nil {
		log.Fatalf("❌ Lỗi khởi tạo Google Sheets: %v", err)
	}

	// 3. Định tuyến (Router)
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handlers.HandleLogin(w, r, authSvc, sheetSvc)
	})

	http.HandleFunc("/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Lấy SpreadsheetID từ header hoặc body (Tùy logic client)
		// Tạm thời hardcode để test hoặc lấy từ request
		sid := "YOUR_SPREADSHEET_ID" 
		handlers.HandleUpdate(w, r, sheetSvc, sid)
	})

	// ... Các handler khác ...

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("TikTok Server is Running! 🚀"))
	})

	// 4. CẤU HÌNH CỔNG (QUAN TRỌNG NHẤT)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Mặc định nếu không có biến môi trường
		log.Printf("⚠️ Không tìm thấy biến PORT, dùng mặc định: %s", port)
	}

	log.Printf("🚀 Server đang chạy tại cổng :%s", port)
	
	// Lắng nghe tại 0.0.0.0 (Bắt buộc cho Docker/Cloud Run)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("❌ Server chết: %v", err)
	}
}
