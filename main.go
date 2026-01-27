package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"tiktok-server/internal/auth"
	"tiktok-server/internal/handlers"
	"tiktok-server/internal/queue"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

// Rate Limiter Simple (Map-based)
var (
	globalRateLimit = 0
	globalLastReset = time.Now()
	globalMutex     sync.Mutex
	tokenLimit      = sync.Map{} // Token -> {count, lastReset}
)

func checkRateLimit(token string) bool {
	// 1. Global (1000 req/s)
	globalMutex.Lock()
	if time.Since(globalLastReset) > time.Second {
		globalRateLimit = 0
		globalLastReset = time.Now()
	}
	globalRateLimit++
	if globalRateLimit > 1000 {
		globalMutex.Unlock()
		return false
	}
	globalMutex.Unlock()

	// 2. Token (5 req/s)
	// (Đơn giản hóa: bỏ qua để code gọn, triển khai sau nếu cần)
	return true
}

func main() {
	port := os.Getenv("PORT")
	if port == "" { port = "8080" }

	authSvc, err := auth.NewAuthenticator()
	if err != nil { log.Fatalf("Auth Init Error: %v", err) }
	
	sheetSvc, err := sheets.NewService()
	if err != nil { log.Fatalf("Sheet Init Error: %v", err) }

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// CORS
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.Write([]byte("TikTok Server V243 (Go) Running..."))
			return
		}

		if !checkRateLimit("global") {
			http.Error(w, `{"status":"false","messenger":"Server Busy"}`, 503)
			return
		}

		// --- SMART PIGGYBACK (URGENT FLUSH) ---
		// Kiểm tra tất cả các Queue, nếu có Queue nào quá tải -> Ép ghi
		queue.GlobalQueues.Range(func(key, value interface{}) bool {
			q := value.(*queue.QueueManager)
			if q.GetPendingCount() > 100 && !q.IsFlushing {
				log.Printf("⚠️ [URGENT FLUSH] SID %v has >100 pending rows", key)
				q.Flush(false)
			}
			return true
		})

		// Read Body safely
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		
		var body map[string]interface{}
		json.Unmarshal(bodyBytes, &body)
		
		reqType, _ := body["type"].(string)
		token, _ := body["token"].(string)

		// Auth
		valid, tokenData, msg := authSvc.VerifyToken(token)
		if !valid {
			utils.JSONResponse(w, "false", msg, nil)
			return
		}
		
		sid := tokenData.SpreadsheetID

		// Router
		switch reqType {
		case "login", "register", "auto", "view":
			handlers.HandleLogin(w, r, sheetSvc, sid)
		case "updated":
			handlers.HandleUpdate(w, r, sheetSvc, sid)
		case "read_mail":
			handlers.HandleReadMail(w, r, sheetSvc, sid)
		case "log_data":
			handlers.HandleLogData(w, r, sheetSvc, sid)
		case "updated_cache": // Clear cache manual
			handlers.HandleUpdatedCache(w, r, sheetSvc)
		case "create_sheets":
			handlers.HandleCreateSheets(w, r, sheetSvc, nil)
		default:
			utils.JSONResponse(w, "false", "Type không hợp lệ", nil)
		}
	})

	log.Printf("🚀 Server running on port :%s", port)
	http.ListenAndServe(":"+port, nil)
}
