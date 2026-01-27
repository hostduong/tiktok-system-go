package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"tiktok-server/internal/auth"
	"tiktok-server/internal/handlers"
	"tiktok-server/internal/sheets"
)

func main() {
	// 1. Cấu hình cổng (QUAN TRỌNG CHO CLOUD RUN)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Biến lưu lỗi khởi động
	var initErr error
	var authSvc *auth.Authenticator
	var sheetSvc *sheets.Service

	// 2. Khởi tạo Auth (Kết nối Database)
	authSvc, err := auth.NewAuthenticator()
	if err != nil {
		fmt.Printf("⚠️ LỖI AUTH: %v\n", err)
		initErr = err
	}

	// 3. Khởi tạo Sheets
	if initErr == nil {
		sheetSvc, err = sheets.NewService()
		if err != nil {
			fmt.Printf("⚠️ LỖI SHEETS: %v\n", err)
			initErr = err
		}
	}

	// 4. Định tuyến Handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Đây là endpoint chính nhận mọi request (giống mainApi trong Node.js)
			
			// Nếu server đang lỗi cấu hình -> Trả về lỗi 500 JSON
			if initErr != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, `{"status": "false", "messenger": "Lỗi khởi động Server: %v"}`, initErr)
				return
			}

			// Routing dựa trên "type" trong body (Login, Update, ReadMail...)
			// Tạm thời trỏ hết vào HandleLogin để test Auth trước, 
			// Sau này bạn sẽ chia case trong file handlers
			handlers.HandleLogin(w, r, authSvc, sheetSvc)
			return
		}

		// GET Request (Trình duyệt)
		if initErr != nil {
			fmt.Fprintf(w, "❌ SERVER LỖI: %v", initErr)
		} else {
			w.Write([]byte("TikTok Server V243 (Go Edition) is Running! 🚀"))
		}
	})

	// Endpoint phụ (nếu cần)
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if initErr != nil { http.Error(w, "Config Error", 500); return }
		handlers.HandleLogin(w, r, authSvc, sheetSvc)
	})

	log.Printf("🚀 Server đang lắng nghe port :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server chết: %v", err)
	}
}
