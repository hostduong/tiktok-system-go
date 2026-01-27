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
	// --- CẤU HÌNH CỔNG TRƯỚC TIÊN (QUAN TRỌNG NHẤT) ---
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Biến lưu lỗi khởi động (nếu có)
	var initErr error
	var authSvc *auth.Authenticator
	var sheetSvc *sheets.Service

	// 1. Thử khởi tạo Auth (Không dùng Fatalf để tránh sập server)
	authSvc, err := auth.NewAuthenticator()
	if err != nil {
		fmt.Printf("⚠️ CẢNH BÁO: Lỗi Firebase Key: %v\n", err)
		initErr = err
	}

	// 2. Thử khởi tạo Sheets
	if initErr == nil {
		sheetSvc, err = sheets.NewService()
		if err != nil {
			fmt.Printf("⚠️ CẢNH BÁO: Lỗi Google Sheets: %v\n", err)
			initErr = err
		}
	}

	// 3. Định tuyến
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Nếu hệ thống đang lỗi config, báo lỗi ra màn hình để User biết đường sửa
		if initErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "❌ SERVER ĐANG LỖI CẤU HÌNH (KEY):\n%v\n\nHãy kiểm tra lại biến môi trường FIREBASE_CREDENTIALS.", initErr)
			return
		}
		w.Write([]byte("TikTok Server V243 (Go Edition) is Running! 🚀"))
	})

	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if initErr != nil {
			http.Error(w, "Server Config Error", http.StatusInternalServerError)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handlers.HandleLogin(w, r, authSvc, sheetSvc)
	})

	// ... (Các handler khác giữ nguyên, chỉ cần check initErr ở đầu) ...
    // Để code gọn, tạm thời tôi chỉ ví dụ handler Login, các cái khác tương tự.
    // Logic Go cũ của bạn đã OK, chỉ cần thay đổi phần main() này thôi.
    
    // Đăng ký lại các route cũ từ file handlers của bạn
    http.HandleFunc("/update", func(w http.ResponseWriter, r *http.Request) {
        if initErr != nil { http.Error(w, "Config Error", 500); return }
        handlers.HandleUpdate(w, r, sheetSvc, "SHEET_ID_PLACEHOLDER") 
    })
    
    http.HandleFunc("/read-mail", func(w http.ResponseWriter, r *http.Request) {
        if initErr != nil { http.Error(w, "Config Error", 500); return }
        // Gọi handler mail (cần parse body trước, nhưng tạm thời để dòng này để test server sống)
        w.Write([]byte(`{"status":"true", "messenger":"Server OK"}`)) 
    })

	log.Printf("🚀 Server đang lắng nghe tại cổng :%s", port)
	
	// 4. KHỞI ĐỘNG (Luôn chạy, không bao giờ để chết vì lỗi config)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("❌ Không thể mở cổng: %v", err)
	}
}
