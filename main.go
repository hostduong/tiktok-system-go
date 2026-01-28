package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	fmt.Println("🚀 [STARTUP] Starting System V243...")

	// 1. Lấy Credentials từ ENV
	rawCred := os.Getenv("FIREBASE_CREDENTIALS")
	if rawCred == "" {
		log.Fatal("❌ [CRITICAL] Missing FIREBASE_CREDENTIALS env var.")
	}

	fmt.Printf("ℹ️ [INFO] Raw Env Length: %d\n", len(rawCred))

	// 2. 🔥 LOGIC THÔNG MINH: Tự động trích xuất JSON chuẩn
	var credJSON []byte
	
	// Bước 1: Thử decode Base64 trước (Trường hợp user dùng Base64)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawCred))
	if err == nil && len(decoded) > 0 && strings.Contains(string(decoded), "{") {
		fmt.Println("✅ [INFO] Detected & Decoded Base64 Credentials.")
		credJSON = decoded
	} else {
		// Bước 2: Nếu không phải Base64, xử lý dạng Text/JSON
		// Thuật toán: Tìm dấu { đầu tiên và dấu } cuối cùng
		start := strings.Index(rawCred, "{")
		end := strings.LastIndex(rawCred, "}")

		if start != -1 && end != -1 && end > start {
			// Cắt bỏ mọi ký tự rác (ngoặc kép, khoảng trắng) bao quanh
			jsonContent := rawCred[start : end+1]
			fmt.Println("✅ [INFO] Extracted valid JSON content from environment variable.")
			credJSON = []byte(jsonContent)
		} else {
			// Fallback: Dùng nguyên gốc nếu không tìm thấy cấu trúc JSON
			fmt.Println("⚠️ [WARN] Could not find JSON structure '{...}'. Using raw value.")
			credJSON = []byte(rawCred)
		}
	}

	// 3. Khởi tạo Service
	// Lưu ý: service_auth.go PHẢI LÀ PHIÊN BẢN V4 (như đã gửi trước đó)
	fmt.Println("🔄 [INIT] Connecting to Firebase...")
	InitFirebase(credJSON)
	
	fmt.Println("🔄 [INIT] Connecting to Google Sheets...")
	InitGoogleService(credJSON)

	// 4. Router
	mux := http.NewServeMux()
	enableCORS := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/tool/login", enableCORS(HandleAccountAction))
	mux.HandleFunc("/tool/updated", enableCORS(HandleUpdateData))
	mux.HandleFunc("/tool/search", enableCORS(HandleSearchData))
	mux.HandleFunc("/tool/log", enableCORS(HandleLogData))
	mux.HandleFunc("/tool/read-mail", enableCORS(HandleReadMail))
	mux.HandleFunc("/tool/create-sheets", enableCORS(HandleCreateSheets))
	mux.HandleFunc("/tool/updated-cache", enableCORS(HandleClearCache))

	// 5. Start Server
	port := os.Getenv("PORT")
	if port == "" { port = "8080" }
	
	server := &http.Server{Addr: ":" + port, Handler: mux}

	go func() {
		fmt.Printf("✅ [READY] Server listening on port %s\n", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ [SERVER ERROR] %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	fmt.Println("🛑 [SIGTERM] Shutting down...")
	STATE.QueueMutex.Lock()
	for sid := range STATE.WriteQueue { FlushQueue(sid, true) }
	STATE.QueueMutex.Unlock()
	fmt.Println("✅ Shutdown complete.")
}
