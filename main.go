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

	rawCred := os.Getenv("FIREBASE_CREDENTIALS")
	if rawCred == "" {
		log.Fatal("❌ [CRITICAL] Missing FIREBASE_CREDENTIALS env var.")
	}

	var credJSON []byte
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawCred))
	if err == nil && len(decoded) > 0 && strings.Contains(string(decoded), "{") {
		fmt.Println("✅ [INFO] Detected & Decoded Base64 Credentials.")
		credJSON = decoded
	} else {
		start := strings.Index(rawCred, "{")
		end := strings.LastIndex(rawCred, "}")
		if start != -1 && end != -1 && end > start {
			credJSON = []byte(rawCred[start : end+1])
			fmt.Println("✅ [INFO] Extracted valid JSON content.")
		} else {
			credJSON = []byte(rawCred)
		}
	}

	// 🔥 FIX: Gọi đúng tên hàm InitAuthService (file service_auth.go)
	fmt.Println("🔄 [INIT] Connecting to Firebase...")
	InitAuthService(credJSON) 
	
	fmt.Println("🔄 [INIT] Connecting to Google Sheets...")
	InitGoogleService(credJSON)

	mux := http.NewServeMux()
	
	// Middleware CORS & Auth
	// Logic: EnableCORS -> AuthMiddleware -> Handler
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// CORS
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// Auth
			AuthMiddleware(http.HandlerFunc(h)).ServeHTTP(w, r)
		}
	}

	// 🔥 Các hàm này giờ đã có trong handler_login.go, handler_update.go, handler_extra.go
	mux.HandleFunc("/tool/login", wrap(HandleAccountAction))
	mux.HandleFunc("/tool/updated", wrap(HandleUpdateData))
	mux.HandleFunc("/tool/search", wrap(HandleSearchData))
	mux.HandleFunc("/tool/log", wrap(HandleLogData))
	mux.HandleFunc("/tool/read-mail", wrap(HandleReadMail))
	mux.HandleFunc("/tool/create-sheets", wrap(HandleCreateSheets))
	mux.HandleFunc("/tool/updated-cache", wrap(HandleClearCache))

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
	// Flush logic
	STATE.QueueMutex.Lock()
	for sid := range STATE.WriteQueue { FlushQueue(sid, true) }
	STATE.QueueMutex.Unlock()
	fmt.Println("✅ Shutdown complete.")
}
