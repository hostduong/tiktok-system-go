package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	GLOBAL_MAX_REQ int   // Max request toàn server / giây
	TOKEN_MAX_REQ  int   // Max request mỗi token / giây
	WINDOW_MS      int64 // Cửa sổ thời gian (ms)
	MIN_LENGTH     int   // Độ dài tối thiểu
	CACHE_TTL_MS   int64 // Thời gian cache mặc định (1 giờ)
	BLOCK_TTL_MS   int64 // Thời gian block token sai (1 phút)
}{
	GLOBAL_MAX_REQ: 1000,
	TOKEN_MAX_REQ:  5,
	WINDOW_MS:      1000,
	MIN_LENGTH:     10,
	CACHE_TTL_MS:   3600000, // 60 phút
	BLOCK_TTL_MS:   60000,   // 60 giây
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
	fmt.Println("✅ Firebase Service initialized (V4) - Secure Edition.")
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// [LỚP 0] Global Rate Limit (Hard Limit)
		if !CheckGlobalRateLimit() {
			http.Error(w, `{"status":"false","messenger":"Server Busy (Global Limit)"}`, 503)
			return
		}

		if firebaseDB == nil {
			http.Error(w, `{"status":"false","messenger":"Database Connecting..."}`, 503)
			return
		}

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

		tokenRaw, _ := bodyMap["token"].(string)
		tokenStr := strings.TrimSpace(tokenRaw)
		
		// [LỚP 1] Check Token (RAM -> Firebase -> Cache)
		authRes := CheckToken(tokenStr)
		if !authRes.IsValid {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": authRes.Messenger})
			return
		}

		// [LỚP 2] User Rate Limit (Soft Limit)
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
// 🛡️ PHẦN 2: LOGIC CHECK TOKEN & TIME PARSER (OPTIMIZED)
// =================================================================================================

func CheckToken(token string) AuthResult {
	if token == "" || len(token) < TOKEN_RULES.MIN_LENGTH {
		return AuthResult{IsValid: false, Messenger: "Token không hợp lệ"}
	}

	now := time.Now().UnixMilli()

	// 1. Kiểm tra Cache RAM
	STATE.TokenMutex.RLock()
	cached, exists := STATE.TokenCache[token]
	STATE.TokenMutex.RUnlock()

	if exists {
		// Cache chặn (Negative Cache)
		if cached.IsInvalid {
			if now < cached.ExpiryTime {
				return AuthResult{IsValid: false, Messenger: cached.Msg}
			}
			deleteTokenCache(token) // Hết hạn block -> Xóa để check lại
		} else {
			// Cache hợp lệ (Positive Cache)
			if now < cached.ExpiryTime {
				return AuthResult{IsValid: true, SpreadsheetID: cached.Data.SpreadsheetID, Data: cached.Data.Data}
			}
			deleteTokenCache(token) // Hết hạn cache -> Xóa để refresh
		}
	}

	// 2. Kiểm tra Firebase
	if firebaseDB == nil {
		return AuthResult{IsValid: false, Messenger: "Database chưa sẵn sàng"}
	}

	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	var data map[string]interface{}
	if err := ref.Get(context.Background(), &data); err != nil {
		log.Printf("❌ [FIREBASE ERROR] %v", err)
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Database"}
	}

	// Xử lý Negative Cache (Chặn spam token rác)
	if data == nil {
		setCache(token, nil, true, "Token không tồn tại", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	if data["expired"] == nil || data["spreadsheetId"] == nil {
		setCache(token, nil, true, "Token lỗi data", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token lỗi data"}
	}

	// 3. Kiểm tra Hạn sử dụng (Smart Time)
	expStr := fmt.Sprintf("%v", data["expired"])
	expTime := parseSmartTime(expStr)
	
	timeLeft := expTime.Sub(time.Now()).Milliseconds()

	// Nếu parse lỗi (time.Zero) hoặc đã hết hạn
	if expTime.IsZero() || timeLeft <= 0 {
		setCache(token, nil, true, "Token hết hạn", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token hết hạn"}
	}

	// 4. Cache thành công (Positive Cache) - Logic TTL chuẩn bảo mật
	sid := fmt.Sprintf("%v", data["spreadsheetId"])
	
	// TTL = Min(Cấu hình, Thời gian sống còn lại)
	// Tránh trường hợp token còn 10s nhưng cache lưu 60s -> Zombie Token
	ttl := TOKEN_RULES.CACHE_TTL_MS
	if ttl > timeLeft {
		ttl = timeLeft
	}

	validData := TokenData{
		Token:         token,
		SpreadsheetID: sid,
		Data:          data,
		Expired:       expStr,
	}
	setCache(token, &validData, false, "", ttl)

	return AuthResult{IsValid: true, SpreadsheetID: sid, Data: data}
}

// 🔥 PARSE TIME THÔNG MINH (UPDATED)
func parseSmartTime(dateStr string) time.Time {
	vnZone := time.FixedZone("UTC+7", 7*3600)
	s := strings.TrimSpace(dateStr)

	// 1️⃣ Numeric Check (Ưu tiên số 1 để tránh nhầm date string)
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		// Ngưỡng 1e11 (100 tỷ):
		// - Seconds: 100 tỷ giây ~ Năm 5138 (Quá xa -> Chắc chắn không phải giây hiện tại)
		// - Millis:  100 tỷ ms   ~ Năm 1973 (Hợp lý cho timestamp cũ, nhưng thường timestamp hiện tại > 1.7e12)
		// => Nếu > 1e11 thì chắc chắn là Milliseconds.
		if ts > 100000000000 { 
			return time.UnixMilli(ts).In(vnZone)
		}
		return time.Unix(ts, 0).In(vnZone)
	}

	// 2️⃣ ISO-8601 / RFC3339 (Chuẩn quốc tế)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(vnZone)
	}
	// Fallback: ISO thiếu Timezone (vd: 2026-01-29T06:03:55) -> Gán VN Zone
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, vnZone); err == nil {
		return t
	}

	// 3️⃣ Date-Only Logic (An toàn hơn)
	// Chỉ cộng giờ nếu có dấu phân cách (- hoặc /) VÀ KHÔNG có giờ (:)
	// Tránh trường hợp chuỗi rác hoặc format lạ
	hasSep := strings.Contains(s, "/") || strings.Contains(s, "-")
	hasTime := strings.Contains(s, ":")
	
	if hasSep && !hasTime {
		s += " 23:59:59"
	}

	// 4️⃣ Custom VN Formats
	layouts := []string{
		"02/01/2006 15:04:05", // dd/MM/yyyy HH:mm:ss
		"02-01-2006 15:04:05", // dd-MM-yyyy HH:mm:ss
		"2006-01-02 15:04:05", // yyyy-MM-dd HH:mm:ss
	}

	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, vnZone); err == nil {
			return t
		}
	}

	// 5️⃣ Fail -> Hết hạn (Time Zero)
	return time.Time{}
}

// =================================================================================================
// ⚙️ HELPER FUNCTIONS
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
	defer STATE.TokenMutex.Unlock() // ✅ Defer chuẩn style Go
	
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
	defer STATE.TokenMutex.Unlock() // ✅ Defer chuẩn style Go
	delete(STATE.TokenCache, token)
}
