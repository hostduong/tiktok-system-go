package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"tiktok-server/internal/auth"
	"tiktok-server/internal/handlers"
	"tiktok-server/internal/queue"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

// --- CẤU HÌNH ---
const (
	GlobalMaxReq = 1000 // 1000 req/s
	TokenMaxReq  = 5    // 5 req/s/token
)

// --- RATE LIMITER ---
type RateLimiter struct {
	sync.Mutex
	GlobalCount int
	LastReset   time.Time
	TokenStats  map[string]*TokenStat
}

type TokenStat struct {
	Count    int
	LastSeen time.Time
	BanUntil time.Time
}

var limiter = &RateLimiter{
	TokenStats: make(map[string]*TokenStat),
	LastReset:  time.Now(),
}

// --- MAIN ---
func main() {
	log.Println("🔌 Đang khởi động TikTok Server V300 (Go)...")
	
	// 1. Khởi tạo Services
	authSvc, err := auth.NewAuthenticator()
	if err != nil { log.Fatalf("❌ Lỗi Firebase: %v", err) }

	sheetSvc, err := sheets.NewService()
	if err != nil { log.Fatalf("❌ Lỗi Google Sheets: %v", err) }

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }

	// 2. Routing
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mainHandler(w, r, authSvc, sheetSvc)
	})

	// 3. Graceful Shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("🛑 [SHUTDOWN] Đang tắt server. Ép xả toàn bộ hàng đợi...")
		var wg sync.WaitGroup
		queue.GlobalQueues.Range(func(key, value interface{}) bool {
			q := value.(*queue.QueueManager)
			wg.Add(1)
			go func() {
				defer wg.Done()
				q.Flush(true) // Force flush
			}()
			return true
		})
		
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()

		select {
		case <-done: log.Println("✅ [SUCCESS] Dữ liệu đã an toàn.")
		case <-time.After(8 * time.Second): log.Println("⚠️ [TIMEOUT] Hết giờ! Buộc phải tắt.")
		}
		os.Exit(0)
	}()

	log.Printf("🚀 Server đang chạy tại port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil { log.Fatal(err) }
}

// --- HANDLER ---
func mainHandler(w http.ResponseWriter, r *http.Request, authSvc *auth.Authenticator, sheetSvc *sheets.Service) {
	// CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Global Rate Limit
	if !checkGlobalLimit() {
		http.Error(w, `{"status":"false","messenger":"Server busy (503)"}`, 503)
		return
	}

	// Đọc Body
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var baseReq struct {
		Type     string `json:"type"`
		Token    string `json:"token"`
		DeviceId string `json:"deviceId"`
	}
	if err := json.Unmarshal(bodyBytes, &baseReq); err != nil {
		utils.JSONResponse(w, "false", "JSON Error", nil)
		return
	}

	// Auth & Routing
	if baseReq.Type != "updated_cache" {
		if !checkTokenLimit(baseReq.Token) {
			utils.JSONResponse(w, "false", "Token bị giới hạn (Spam)", nil)
			return
		}

		isValid, tokenData, msg := authSvc.VerifyToken(baseReq.Token)
		if !isValid {
			utils.JSONResponse(w, "false", msg, nil)
			return
		}
		sid := tokenData.SpreadsheetID

		switch baseReq.Type {
		case "login", "register", "auto", "view":
			if baseReq.DeviceId == "" {
				utils.JSONResponse(w, "false", "Thiếu deviceId", nil)
				return
			}
			handlers.HandleLogin(w, r, sheetSvc, sid)

		case "updated":
			handlers.HandleUpdate(w, r, sheetSvc, sid)

		case "log_data":
			handlers.HandleLogData(w, r, sheetSvc, sid)
			
		case "create_sheets":
			handlers.HandleCreateSheets(w, r, sheetSvc, map[string]interface{}{"spreadsheetId": sid})

		case "read_mail":
			// 🔥 Đã trỏ đúng vào hàm HandleReadMail (file mail.go)
			handlers.HandleReadMail(w, r, sheetSvc, sid)

		default:
			utils.JSONResponse(w, "false", "Type không hợp lệ", nil)
		}
	} else {
		handlers.HandleUpdatedCache(w, r, sheetSvc)
	}
}

// --- HELPER ---
func checkGlobalLimit() bool {
	limiter.Lock(); defer limiter.Unlock()
	now := time.Now()
	if now.Sub(limiter.LastReset) > time.Second {
		limiter.GlobalCount = 0; limiter.LastReset = now
	}
	limiter.GlobalCount++
	return limiter.GlobalCount <= GlobalMaxReq
}

func checkTokenLimit(token string) bool {
	limiter.Lock(); defer limiter.Unlock()
	now := time.Now()
	stat, exists := limiter.TokenStats[token]
	if !exists {
		stat = &TokenStat{LastSeen: now}
		limiter.TokenStats[token] = stat
	}
	if !stat.BanUntil.IsZero() && now.Before(stat.BanUntil) { return false }
	if now.Sub(stat.LastSeen) > time.Second {
		stat.Count = 0; stat.LastSeen = now
	}
	stat.Count++
	if stat.Count > TokenMaxReq {
		stat.BanUntil = now.Add(5 * time.Minute)
		return false
	}
	return true
}
