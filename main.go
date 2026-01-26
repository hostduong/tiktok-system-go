package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// --- CẤU HÌNH TOÀN CỤC ---
const (
	GlobalMaxReq = 1000 // 1000 req/s
	TokenMaxReq  = 5    // 5 req/s/token
)

// --- RATE LIMITER (Bộ đếm) ---
type RateLimiter struct {
	sync.Mutex
	GlobalCount   int
	LastReset     time.Time
	
	// Map[Token] -> {Count, LastSeen, BanUntil}
	TokenStats map[string]*TokenStat
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

// --- MAIN FUNCTION ---
func main() {
	// 1. Khởi tạo Services
	log.Println("🔌 Đang kết nối Firebase & Google Sheets...")
	
	authSvc, err := auth.NewAuthenticator()
	if err != nil {
		log.Fatalf("❌ Lỗi Firebase: %v", err)
	}

	sheetSvc, err := sheets.NewService()
	if err != nil {
		log.Fatalf("❌ Lỗi Google Sheets: %v", err)
	}

	// 2. Setup Server & Port
	port := os.Getenv("PORT")
	if port == "" { port = "8080" }

	// 3. Routing
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mainHandler(w, r, authSvc, sheetSvc)
	})

	// 4. Graceful Shutdown (Bắt sự kiện tắt server để lưu dữ liệu)
	// Đây là logic dòng [451-455] của Node.js
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("🛑 [SHUTDOWN] Đang tắt server. Ép xả toàn bộ hàng đợi...")
		
		var wg sync.WaitGroup
		// Duyệt qua tất cả Queue đang hoạt động
		queue.GlobalQueues.Range(func(key, value interface{}) bool {
			q := value.(*queue.QueueManager)
			wg.Add(1)
			go func() {
				defer wg.Done()
				q.Flush(true) // Force flush
			}()
			return true
		})
		
		// Đợi tối đa 8 giây (Logic Node.js)
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			log.Println("✅ [SUCCESS] Dữ liệu đã an toàn.")
		case <-time.After(8 * time.Second):
			log.Println("⚠️ [TIMEOUT] Hết giờ! Buộc phải tắt.")
		}
		os.Exit(0)
	}()

	log.Printf("🚀 Server TikTok V300 (Go) đang chạy tại port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

// --- MAIN HANDLER (Logic điều phối request) ---
func mainHandler(w http.ResponseWriter, r *http.Request, authSvc *auth.Authenticator, sheetSvc *sheets.Service) {
	// 1. CORS
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

	// 2. Global Rate Limit (Hard Limit)
	if !checkGlobalLimit() {
		http.Error(w, `{"status":"false","messenger":"Server busy (503)"}`, 503)
		return
	}

	// 3. Smart Piggyback (Kích hoạt Queue chạy ngầm nếu request đang đông)
	// Logic [432-438]: Duyệt qua các queue và checkTrigger
	// Go làm việc này tự động trong queue/worker.go mỗi khi Enqueue, 
	// nhưng ta có thể kích hoạt thêm ở đây nếu muốn chắc chắn.
	// (Go Worker tự chạy ngầm nên bước này nhẹ nhàng hơn Node.js nhiều)

	// 4. Đọc Body (Để lấy Token & Type)
	// Lưu ý: Đọc xong phải ghi lại vào r.Body để các handler sau đọc tiếp được
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// Decode sơ bộ
	var baseReq struct {
		Type     string `json:"type"`
		Token    string `json:"token"`
		DeviceId string `json:"deviceId"`
	}
	if err := json.Unmarshal(bodyBytes, &baseReq); err != nil {
		utils.JSONResponse(w, "false", "JSON Error", nil)
		return
	}

	// 5. Auth & Token Rate Limit (Soft Limit)
	// Logic [442-443]
	if baseReq.Type != "updated_cache" { // updated_cache có thể không cần token hoặc token admin
		// Check Rate Limit Token
		if !checkTokenLimit(baseReq.Token) {
			utils.JSONResponse(w, "false", "Token bị giới hạn (Spam)", nil)
			return
		}

		// Verify Token Firebase
		isValid, tokenData, msg := authSvc.VerifyToken(baseReq.Token)
		if !isValid {
			utils.JSONResponse(w, "false", msg, nil)
			return
		}

		// Tạo Context có SpreadsheetID (Cách Go truyền dữ liệu)
		// Nhưng ở đây ta truyền thẳng vào hàm cho đơn giản
		sid := tokenData.SpreadsheetID
		
		// Logic Routing [445-450]
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
			// Riêng create_sheets cần xử lý update ID nếu khác nhau (Logic misc.go)
			handlers.HandleCreateSheets(w, r, sheetSvc, map[string]interface{}{"spreadsheetId": sid})

		case "read_mail":
			handlers.HandleReadMail(w, r, sheetSvc, sid)

		default:
			utils.JSONResponse(w, "false", "Type không hợp lệ", nil)
		}
	} else {
		// Trường hợp updated_cache
		handlers.HandleUpdatedCache(w, r, sheetSvc)
	}
}

// --- HELPER FUNCTIONS ---

func checkGlobalLimit() bool {
	limiter.Lock()
	defer limiter.Unlock()

	now := time.Now()
	// Reset mỗi giây
	if now.Sub(limiter.LastReset) > time.Second {
		limiter.GlobalCount = 0
		limiter.LastReset = now
	}

	limiter.GlobalCount++
	return limiter.GlobalCount <= GlobalMaxReq
}

func checkTokenLimit(token string) bool {
	limiter.Lock()
	defer limiter.Unlock()

	now := time.Now()
	stat, exists := limiter.TokenStats[token]
	if !exists {
		stat = &TokenStat{LastSeen: now}
		limiter.TokenStats[token] = stat
	}

	// Check Ban
	if !stat.BanUntil.IsZero() && now.Before(stat.BanUntil) {
		return false
	}

	// Reset mỗi giây
	if now.Sub(stat.LastSeen) > time.Second {
		stat.Count = 0
		stat.LastSeen = now
	}

	stat.Count++
	
	// Logic Ban 5 phút nếu spam quá đà (Ở đây làm đơn giản count > limit)
	if stat.Count > TokenMaxReq {
		// Ban 5 phút
		stat.BanUntil = now.Add(5 * time.Minute)
		return false
	}

	return true
}
