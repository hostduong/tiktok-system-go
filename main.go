package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	// 🟢 STEP 1: Bắt đầu khởi động
	fmt.Println("🚀 [STARTUP] Starting System V243...")

	// 1. Lấy Credentials từ ENV
	rawCred := os.Getenv("FIREBASE_CREDENTIALS")
	if rawCred == "" {
		log.Fatal("❌ [CRITICAL ERROR] Missing FIREBASE_CREDENTIALS environment variable. Please check Cloud Run Variables.")
	}

	// 🔥 FIX QUAN TRỌNG: Làm sạch chuỗi JSON
	// Nhiều trường hợp copy paste bị dính dấu " ở đầu đuôi hoặc khoảng trắng thừa gây lỗi JSON parse
	cleanCred := strings.TrimSpace(rawCred)
	if strings.HasPrefix(cleanCred, "\"") && strings.HasSuffix(cleanCred, "\"") {
		cleanCred = strings.Trim(cleanCred, "\"")
		fmt.Println("⚠️ [WARNING] Detected and removed extra quotes from FIREBASE_CREDENTIALS.")
	}
	
	fmt.Printf("ℹ️ [INFO] Credentials length: %d characters\n", len(cleanCred))
	credJSON := []byte(cleanCred)

	// 2. Khởi tạo Service (Nếu lỗi sẽ in ra lý do cụ thể ở đây)
	fmt.Println("🔄 [INIT] Connecting to Firebase...")
	InitFirebase(credJSON)
	
	fmt.Println("🔄 [INIT] Connecting to Google Sheets...")
	InitGoogleService(credJSON)

	// 3. Router
	mux := http.NewServeMux()

	// Cors Middleware Wrapper
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

	// 4. Start Server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	server := &http.Server{Addr: ":" + port, Handler: mux}

	// 5. Graceful Shutdown Setup
	go func() {
		fmt.Printf("✅ [READY] Server listening on port %s\n", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Nếu port bị chiếm hoặc không bind được, log lỗi ra đây
			log.Fatalf("❌ [SERVER ERROR] ListenAndServe: %v", err)
		}
	}()

	// Wait for SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	fmt.Println("🛑 [SIGTERM] Shutting down...")
	
	// Force Flush All Queues
	STATE.QueueMutex.Lock()
	for sid := range STATE.WriteQueue {
		FlushQueue(sid, true)
	}
	STATE.QueueMutex.Unlock()
	
	fmt.Println("✅ Shutdown complete.")
}
