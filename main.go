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
	var credJSON []byte

	// 🔥 FIX: Không Fatal nếu thiếu biến môi trường, chỉ Warn
	if rawCred == "" {
		fmt.Println("⚠️ [WARN] Missing FIREBASE_CREDENTIALS env var. System will start in limited mode.")
	} else {
		fmt.Printf("ℹ️ [INFO] Raw Env Length: %d\n", len(rawCred))
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawCred))
		if err == nil && len(decoded) > 0 && strings.Contains(string(decoded), "{") {
			fmt.Println("✅ [INFO] Detected & Decoded Base64 Credentials.")
			credJSON = decoded
		} else {
			start := strings.Index(rawCred, "{")
			end := strings.LastIndex(rawCred, "}")
			if start != -1 && end != -1 && end > start {
				jsonContent := rawCred[start : end+1]
				fmt.Println("✅ [INFO] Extracted valid JSON content.")
				credJSON = []byte(jsonContent)
			} else {
				fmt.Println("⚠️ [WARN] Raw JSON might be invalid.")
				credJSON = []byte(rawCred)
			}
		}
	}

	fmt.Println("🔄 [INIT] Connecting to Services...")
	// 🔥 Dù credJSON rỗng vẫn gọi hàm init, hàm init mới (ở trên) sẽ xử lý an toàn
	InitAuthService(credJSON) 
	InitGoogleService(credJSON)

	mux := http.NewServeMux()
	
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			AuthMiddleware(http.HandlerFunc(h)).ServeHTTP(w, r)
		}
	}

	mux.HandleFunc("/tool/login", wrap(HandleAccountAction))
	mux.HandleFunc("/tool/updated", wrap(HandleUpdateData))
	mux.HandleFunc("/tool/search", wrap(HandleSearchData))
	mux.HandleFunc("/tool/log", wrap(HandleLogData))
	mux.HandleFunc("/tool/read-mail", wrap(HandleReadMail))
	mux.HandleFunc("/tool/create-sheets", wrap(HandleCreateSheets))
	mux.HandleFunc("/tool/updated-cache", wrap(HandleClearCache))

	// Health Check
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("TikTok System Go V243 is Ready!"))
	})

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }
	
	server := &http.Server{Addr: ":" + port, Handler: mux}

	go func() {
		fmt.Printf("✅ [READY] Server listening on port %s\n", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ [SERVER ERROR] %v", err) // Printf thay vì Fatal
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
