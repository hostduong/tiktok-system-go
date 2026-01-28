package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

var firebaseDB *db.Client
var AuthInitError error

// =================================================================================================
// 🔧 CẤU HÌNH TOKEN & RATE LIMIT (Centralized Config)
// =================================================================================================

var TOKEN_RULES = struct {
	// Rate Limit
	GLOBAL_MAX_REQ int   // Max request toàn server / giây
	TOKEN_MAX_REQ  int   // Max request mỗi token / giây
	WINDOW_MS      int64 // Cửa sổ thời gian (ms)

	// Token Config
	MIN_LENGTH   int            // Độ dài tối thiểu
	CACHE_TTL_MS int64          // Thời gian cache token đúng (60 phút)
	BLOCK_TTL_MS int64          // Thời gian block token sai (1 phút)
}{
	GLOBAL_MAX_REQ: 1000,
	TOKEN_MAX_REQ:  5,
	WINDOW_MS:      1000,

	MIN_LENGTH:   10,
	CACHE_TTL_MS: 3600000, // 1 giờ
	BLOCK_TTL_MS: 60000,   // 60 giây
}

// =================================================================================================
// 🚀 PHẦN 1: KHỞI TẠO & MIDDLEWARE
// =================================================================================================

func InitAuthService(credJSON []byte) {
	if len(credJSON) == 0 {
		AuthInitError = fmt.Errorf("Credential Data is empty")
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	ctx := context.Background()
	opt := option.WithCredentialsJSON(credJSON)
	
	conf := &firebase.Config{
		DatabaseURL: "https://hostduong-1991-default-rtdb.asia-southeast1.firebasedatabase.app",
	}

	app, err := firebase.NewApp(ctx, conf, opt)
	if err != nil {
		AuthInitError = fmt.Errorf("Firebase Init Error: %v", err)
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	client, err := app.Database(ctx)
	if err != nil {
		AuthInitError = fmt.Errorf("Firebase DB Error: %v", err)
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	firebaseDB = client
	fmt.Println("✅ Firebase Service initialized (V4) - Smart Time Edition.")
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// [LỚP 0] Global Rate Limit
		if !CheckGlobalRateLimit() {
			http.Error(w, `{"status":"false","messenger":"Server Busy (Global Limit)"}`, 503)
			return
		}

		if firebaseDB == nil {
			http.Error(w, `{"status":"false","messenger":"Database Connecting..."}`, 503)
			return
		}

		// Đọc Body an toàn
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"status":"false","messenger":"Read Body Error"}`, 400)
			return
		}
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var bodyMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
			http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
			return
		}

		// Lấy Token (Giữ nguyên hoa thường để khớp Firebase)
		tokenRaw, _ := bodyMap["token"].(string)
		tokenStr := strings.TrimSpace(tokenRaw)
		
		// [LỚP 1] Check Token (RAM -> Firebase -> Cache)
		authRes := CheckToken(tokenStr)
		if !authRes.IsValid {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": authRes.Messenger})
			return
		}

		// [LỚP 2] User Rate Limit
		if !CheckUserRateLimit(tokenStr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Spam detected (Rate Limit)"})
			return
		}

		ctx := context.WithValue(r.Context(), "tokenData", &TokenData{
			Token:         tokenStr,
			SpreadsheetID: authRes.SpreadsheetID,
			Data:          authRes.Data,
		})

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// =================================================================================================
// 🛡️ PHẦN 2: LOGIC CHECK TOKEN & TIME PARSER (THÔNG MINH)
// =================================================================================================

func CheckToken(token string) AuthResult {
	// 1. Validate sơ bộ
	if token == "" || len(token) < TOKEN_RULES.MIN_LENGTH {
		return AuthResult{IsValid: false, Messenger: "Token không hợp lệ (Quá ngắn)"}
	}

	now := time.Now().UnixMilli()

	// 2. Kiểm tra Cache RAM (Lớp 1)
	STATE.TokenMutex.RLock()
	cached, exists := STATE.TokenCache[token]
	STATE.TokenMutex.RUnlock()

	if exists {
		// Nếu là Cache chặn (Negative Cache)
		if cached.IsInvalid {
			if now < cached.ExpiryTime {
				return AuthResult{IsValid: false, Messenger: cached.Msg}
			}
			// Hết thời gian phạt -> Xóa cache để check lại
			deleteTokenCache(token)
		} else {
			// Token hợp lệ
			if now < cached.ExpiryTime {
				return AuthResult{IsValid: true, SpreadsheetID: cached.Data.SpreadsheetID, Data: cached.Data.Data}
			}
			// Hết hạn cache -> Check lại Firebase cập nhật mới
			deleteTokenCache(token)
		}
	}

	// 3. Kiểm tra Firebase (Lớp 2)
	if firebaseDB == nil {
		return AuthResult{IsValid: false, Messenger: "Database chưa sẵn sàng"}
	}

	// Dùng DataSnapshot (once value) để chắc chắn
	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	var data map[string]interface{}
	
	if err := ref.Get(context.Background(), &data); err != nil {
		log.Printf("❌ [FIREBASE ERROR] %v", err)
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Database"}
	}

	// Cache chặn (Negative Cache) nếu không tìm thấy
	if data == nil {
		setCache(token, nil, true, "Token không tồn tại", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	if data["expired"] == nil || data["spreadsheetId"] == nil {
		setCache(token, nil, true, "Token lỗi data", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token lỗi data"}
	}

	// 4. Kiểm tra Hạn sử dụng (Smart Parse Logic)
	expStr := fmt.Sprintf("%v", data["expired"])
	expTime := parseSmartTime(expStr) // 🔥 Gọi hàm thông minh mới
	
	// Nếu parse thất bại (time.Zero) hoặc đã qua giờ G
	if expTime.IsZero() || time.Now().After(expTime) {
		log.Printf("⚠️ Token Expired/Invalid Time: %s (Raw: %s, Parsed: %v)", token, expStr, expTime)
		setCache(token, nil, true, "Token hết hạn", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token hết hạn"}
	}

	// 5. Cache thành công (Positive Cache)
	sid := fmt.Sprintf("%v", data["spreadsheetId"])
	
	// Tính TTL: Max là CACHE_TTL_MS, hoặc thời gian còn lại của Token
	ttl := TOKEN_RULES.CACHE_TTL_MS
	timeLeft := expTime.Sub(time.Now()).Milliseconds()
	if timeLeft < ttl {
		ttl = timeLeft
	}
	if ttl < 60000 { ttl = 60000 } // Tối thiểu 1 phút

	validData := TokenData{
		Token:         token,
		SpreadsheetID: sid,
		Data:          data,
		Expired:       expStr,
	}
	setCache(token, &validData, false, "", ttl)

	return AuthResult{IsValid: true, SpreadsheetID: sid, Data: data}
}

// 🔥 HÀM PARSE THỜI GIAN THÔNG MINH (THEO ĐỀ XUẤT CỦA BẠN)
func parseSmartTime(dateStr string) time.Time {
	vnZone := time.FixedZone("UTC+7", 7*3600)
	s := strings.TrimSpace(dateStr)

	// 1️⃣ Numeric timestamp (s / ms)
	// Cho phép kiểu số nguyên hoặc chuỗi số
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		if ts > 1e11 { // milliseconds (13 digits)
			return time.UnixMilli(ts).In(vnZone)
		}
		// seconds (10 digits)
		return time.Unix(ts, 0).In(vnZone)
	}

	// 2️⃣ ISO-8601 (RFC3339) - Ưu tiên chuẩn quốc tế
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(vnZone)
	}

	// 3️⃣ Date-only → Cuối ngày
	// Logic đơn giản: Nếu độ dài <= 10 (vd: 29/01/2026), tự động thêm giờ cuối ngày
	if len(s) <= 10 && !strings.Contains(s, ":") {
		s += " 23:59:59"
	}

	// 4️⃣ Custom VN Formats (Có giờ)
	layouts := []string{
		"02/01/2006 15:04:05", // dd/MM/yyyy HH:mm:ss
		"02-01-2006 15:04:05", // dd-MM-yyyy HH:mm:ss
		"2006-01-02 15:04:05", // yyyy-MM-dd HH:mm:ss
	}

	for _, layout := range layouts {
		// ParseInLocation để ép hiểu là giờ VN (+7)
		if t, err := time.ParseInLocation(layout, s, vnZone); err == nil {
			return t
		}
	}

	// 5️⃣ Fail closed -> Trả về time.Zero (IsZero() == true)
	return time.Time{}
}

// =================================================================================================
// ⚙️ PHẦN 3: RATE LIMIT & CACHE HELPERS
// =================================================================================================

func CheckGlobalRateLimit() bool {
	STATE.GlobalCounter.Mutex.Lock()
	defer STATE.GlobalCounter.Mutex.Unlock()

	now := time.Now().UnixMilli()
	if now-STATE.GlobalCounter.LastReset > TOKEN_RULES.WINDOW_MS {
		STATE.GlobalCounter.LastReset = now
		STATE.GlobalCounter.Count = 0
	}
	STATE.GlobalCounter.Count++
	return STATE.GlobalCounter.Count <= TOKEN_RULES.GLOBAL_MAX_REQ
}

func CheckUserRateLimit(token string) bool {
	STATE.RateMutex.Lock()
	defer STATE.RateMutex.Unlock()

	now := time.Now().UnixMilli()
	rec, exists := STATE.RateLimit[token]
	if !exists {
		rec = &RateLimitData{LastReset: now, Count: 0}
		STATE.RateLimit[token] = rec
	}

	if now-rec.LastReset > TOKEN_RULES.WINDOW_MS {
		rec.LastReset = now
		rec.Count = 0
	}
	rec.Count++
	return rec.Count <= TOKEN_RULES.TOKEN_MAX_REQ
}

func setCache(token string, data *TokenData, isInvalid bool, msg string, ttl int64) {
	STATE.TokenMutex.Lock()
	defer STATE.TokenMutex.Unlock()
	
	cached := &CachedToken{
		IsInvalid:  isInvalid,
		Msg:        msg,
		ExpiryTime: time.Now().UnixMilli() + ttl,
	}
	if data != nil {
		cached.Data = *data
	}
	STATE.TokenCache[token] = cached
}

func deleteTokenCache(token string) {
	STATE.TokenMutex.Lock()
	delete(STATE.TokenCache, token)
	STATE.TokenMutex.Unlock()
}
