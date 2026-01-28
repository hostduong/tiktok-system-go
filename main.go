package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// 1. Lấy Credentials từ ENV
	credJSON := []byte(os.Getenv("FIREBASE_CREDENTIALS"))
	if len(credJSON) == 0 {
		log.Fatal("❌ Missing FIREBASE_CREDENTIALS env")
	}

	// 2. Khởi tạo Service
	InitFirebase(credJSON)
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
			// Global Rate Limit Check here if needed
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
		fmt.Printf("🚀 Server running on port %s\n", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Server error: %v", err)
		}
	}()

	// Wait for SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	fmt.Println("🛑 [SIGTERM] Shutting down...")
	
	// Force Flush All Queues
	// Duyệt qua tất cả các queue trong STATE và gọi FlushQueue(sid, true)
	STATE.QueueMutex.Lock()
	for sid := range STATE.WriteQueue {
		FlushQueue(sid, true)
	}
	STATE.QueueMutex.Unlock()
	
	fmt.Println("✅ Shutdown complete.")
}
