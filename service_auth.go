package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	firebase "firebase.google.com/go"
	"firebase.google.com/go/db"
	"google.golang.org/api/option"
)

// Biến global cho Firebase App (Singleton)
var firebaseApp *firebase.App
var firebaseDb *db.Client

// InitFirebase khởi tạo kết nối (Gọi 1 lần ở main.go)
func InitFirebase(credJSON []byte) {
	ctx := context.Background()
	opt := option.WithCredentialsJSON(credJSON)
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		log.Fatalf("❌ Firebase Init Error: %v", err)
	}
	client, err := app.Database(ctx)
	if err != nil {
		log.Fatalf("❌ Firebase DB Error: %v", err)
	}
	
	// Cấu hình HTTP Agent cho Firebase (Tối ưu kết nối)
	// Go Firebase SDK tự động quản lý Pool, nhưng ta set tham số
	// thông qua option nếu cần thiết. Mặc định Go HTTP Client đã tốt.
	
	firebaseApp = app
	firebaseDb = client
	fmt.Println("✅ Firebase initialized.")
}

// =================================================================================================
// 🟢 AUTH CORE: Xử lý đồng bộ Firebase & Kiểm tra Token
// =================================================================================================

type AuthResult struct {
	IsValid       bool
	SpreadsheetID string
	Role          string
	Messenger     string
}

// CheckToken: Hàm kiểm tra chính (Mô phỏng 100% logic Node.js)
func CheckToken(token string) AuthResult {
	// 1. Validate sơ bộ
	if token == "" || len(token) < 50 || len(token) > 200 || !REGEX_TOKEN.MatchString(token) {
		return AuthResult{IsValid: false, Messenger: "Token sai định dạng"}
	}

	// 2. Rate Limit (Lớp bảo vệ 1)
	if !checkRateLimit(token, false) {
		return AuthResult{IsValid: false, Messenger: "Token bị giới hạn tạm thời (Spam)"}
	}

	now := time.Now().UnixMilli()

	[cite_start]// 3. Kiểm tra RAM (Lớp ưu tiên - Cache Hit) [cite: 195-198]
	// Sử dụng RLock để cho phép nhiều request đọc cùng lúc (Nhanh, không chặn)
	STATE.TokenMutex.RLock()
	cached, found := STATE.TokenCache[token]
	STATE.TokenMutex.RUnlock() // Mở khóa ngay sau khi đọc xong

	if found {
		if now < cached.ExpiryTime {
			if cached.IsInvalid {
				return AuthResult{IsValid: false, Messenger: cached.Msg}
			}
			// ✅ Cache Hit: Trả về SpreadsheetID ngay lập tức
			return AuthResult{IsValid: true, SpreadsheetID: cached.Data.SpreadsheetID, Role: cached.Data.Role}
		}
		// Nếu hết hạn -> Xóa khỏi RAM (Lazy delete) -> Để xuống bước gọi Firebase
		STATE.TokenMutex.Lock()
		delete(STATE.TokenCache, token)
		STATE.TokenMutex.Unlock()
	}

	[cite_start]// 4. Kiểm tra Firebase (Lớp dự phòng - Cache Miss) [cite: 199-210]
	// Chỉ chạy vào đây nếu RAM không có dữ liệu
	ref := firebaseDb.NewRef("TOKEN_TIKTOK/" + token)
	var data map[string]interface{}
	
	// Context có Timeout để tránh treo server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := ref.Get(ctx, &data); err != nil {
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Firebase"}
	}

	// Nếu token không tồn tại trên Firebase
	if data == nil {
		checkRateLimit(token, true) // Phạt lỗi
		updateTokenCache(token, TokenData{}, true, "Token không tồn tại", 60000) // Cache lỗi 1 phút
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	// Kiểm tra trường 'expired' (boolean)
	isExpired, _ := data["expired"].(bool)
	if !isExpired {
		checkRateLimit(token, true)
		updateTokenCache(token, TokenData{}, true, "Token lỗi", 60000)
		return AuthResult{IsValid: false, Messenger: "Token lỗi"}
	}

	// 5. Kiểm tra thời gian hết hạn (Xử lý Đa năng theo yêu cầu)
	// Lấy giá trị expiration_time từ Firebase
	expVal := data["expiration_time"] // Có thể là string hoặc number
	expTimeMs := parseExpirationTime(expVal)

	if expTimeMs == 0 || now > expTimeMs {
		updateTokenCache(token, TokenData{}, true, "Token hết hạn", 60000)
		return AuthResult{IsValid: false, Messenger: "Token hết hạn"}
	}

	// 6. Lấy SpreadsheetID và Role
	sid, _ := data["spreadsheetId"].(string)
	role, _ := data["role"].(string) // Optional

	tokenData := TokenData{
		SpreadsheetID: sid,
		Role:          role,
	}

	// 7. Ghi ngược vào RAM (Cache Fill)
	[cite_start]// Tính TTL: Min(Thời gian còn lại của Token, 1 Giờ) [cite: 207]
	ttl := expTimeMs - now
	if ttl > CACHE.TOKEN_TTL_MS {
		ttl = CACHE.TOKEN_TTL_MS
	}
	
	updateTokenCache(token, tokenData, false, "", ttl)

	return AuthResult{IsValid: true, SpreadsheetID: sid, Role: role}
}

// Helper: Cập nhật Cache an toàn với Mutex Lock
func updateTokenCache(token string, data TokenData, isInvalid bool, msg string, ttlMs int64) {
	STATE.TokenMutex.Lock()
	defer STATE.TokenMutex.Unlock()
	
	STATE.TokenCache[token] = &CachedToken{
		Data:       data,
		IsInvalid:  isInvalid,
		Msg:        msg,
		ExpiryTime: time.Now().UnixMilli() + ttlMs,
	}
}

// =================================================================================================
// 🧠 LOGIC XỬ LÝ THỜI GIAN THÔNG MINH (FLEXIBLE DATE PARSER)
// =================================================================================================
func parseExpirationTime(val interface{}) int64 {
	if val == nil {
		return 0
	}

	// Trường hợp 1: Dạng số (Excel Serial hoặc Unix Millis)
	if num, ok := val.(float64); ok {
		// Nếu nhỏ hơn 100000 -> Khả năng là Excel Serial Date (Ví dụ: 45678)
		if num < 200000 { 
			// Convert Excel -> Unix Millis (Trừ 7 tiếng để về logic gốc nếu cần, hoặc để UTC)
			// Logic gốc Node.js: (v - 25569) * 86400000 - (7 * 3600000)
			return int64((num - 25569) * 86400000) - (7 * 3600000)
		}
		// Nếu lớn -> Unix Millis
		return int64(num)
	}

	str, ok := val.(string)
	if !ok {
		return 0
	}
	str = strings.TrimSpace(str)
	if str == "" {
		return 0
	}

	// Chuẩn hóa dấu phân cách: Thay thế '-' và khoảng trắng bằng '/'
	// Ví dụ: "24-11-2099" -> "24/11/2099"
	normalized := strings.ReplaceAll(str, "-", "/")
	normalized = strings.ReplaceAll(normalized, ".", "/")
	
	// Load Timezone VN (UTC+7)
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	if loc == nil {
		loc = time.FixedZone("UTC+7", 7*60*60)
	}

	// Trường hợp 2: Chỉ có ngày (dd/mm/yyyy) -> Set về cuối ngày (23:59:59)
	if len(normalized) <= 10 {
		t, err := time.ParseInLocation("02/01/2006", normalized, loc)
		if err == nil {
			// Cộng thêm để thành 23:59:59
			endOfDay := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, loc)
			return endOfDay.UnixMilli()
		}
	}

	// Trường hợp 3: Ngày giờ đầy đủ (dd/mm/yyyy HH:mm:ss)
	// Thử parse format có dấu cách (đã replace - bằng / ở trên nhưng space giữa ngày và giờ vẫn còn)
	// Cần xử lý lại normalized cho trường hợp space giữa ngày và giờ
	// "24/11/2099 21:18:15"
	t, err := time.ParseInLocation("02/01/2006 15:04:05", normalized, loc)
	if err == nil {
		return t.UnixMilli()
	}

	// Trường hợp 4: ISO 8601 (2099-11-24T21:18:15Z)
	tISO, err := time.Parse(time.RFC3339, str) // RFC3339 tương đương ISO 8601
	if err == nil {
		return tISO.UnixMilli()
	}

	return 0
}

// =================================================================================================
// 🛡️ RATE LIMIT LOGIC (Anti-Spam)
// =================================================================================================
func checkRateLimit(token string, isError bool) bool {
	STATE.RateMutex.Lock()
	defer STATE.RateMutex.Unlock()

	now := time.Now().UnixMilli()
	
	rec, exists := STATE.RateLimit[token]
	if !exists {
		rec = &RateLimitData{
			LastReset: now,
			LastSeen:  now,
		}
		STATE.RateLimit[token] = rec
	}

	rec.LastSeen = now

	// Reset counter nếu qua cửa sổ thời gian
	if now-rec.LastReset > RATE.WINDOW_MS {
		rec.Count = 0
		rec.LastReset = now
	}

	// Kiểm tra Ban
	if rec.BanUntil > 0 && now < rec.BanUntil {
		return false
	}

	rec.Count++
	if isError {
		rec.ErrorCount++
	}

	[cite_start]// Check Limits [cite: 76-78]
	if rec.Count > RATE.TOKEN_MAX_REQ {
		return false
	}

	if rec.ErrorCount > RATE.MAX_ERROR {
		rec.BanUntil = now + RATE.BAN_MS
		return false
	}

	return true
}
