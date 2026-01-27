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
	// 1. Cấu hình cổng (Bắt buộc cho Cloud Run)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var initErr error
	var authSvc *auth.Authenticator
	var sheetSvc *sheets.Service

	// 2. Khởi tạo Auth (Dùng Key JSON để check user bản quyền)
	// Code này sẽ đọc biến môi trường FIREBASE_CREDENTIALS
	authSvc, err := auth.NewAuthenticator()
	if err != nil {
		fmt.Printf("⚠️ LỖI AUTH (Firebase): %v\n", err)
		initErr = err
	}

	// 3. Khởi tạo Sheets (Dùng quyền Server để đọc Excel)
	if initErr == nil {
		sheetSvc, err = sheets.NewService()
		if err != nil {
			fmt.Printf("⚠️ LỖI SHEETS (Google API): %v\n", err)
			initErr = err
		}
	}

	// 4. Định tuyến (Router)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Chỉ nhận POST cho API chính
		if r.Method == http.MethodPost {
			// Nếu server đang lỗi cấu hình -> Báo lỗi JSON
			if initErr != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, `{"status": "false", "messenger": "Lỗi khởi động Server: %v"}`, initErr)
				return
			}

			// Chuyển tiếp vào Handler xử lý chính
			// Ở đây tôi trỏ tạm vào HandleLogin để test, sau này bạn dùng switch-case type
			handlers.HandleLogin(w, r, authSvc, sheetSvc)
			return
		}

		// GET Request (Trang chủ kiểm tra sức khỏe server)
		if initErr != nil {
			fmt.Fprintf(w, "❌ SERVER LỖI: %v", initErr)
		} else {
			w.Write([]byte("TikTok Server V243 (Go Hybrid Auth) is Running! 🚀"))
		}
	})

	log.Printf("🚀 Server đang lắng nghe port :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server chết: %v", err)
	}
}
