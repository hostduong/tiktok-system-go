package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	firebase "firebase.google.com/go"
	"firebase.google.com/go/db"
	"google.golang.org/api/option"
)

// Biến global cho Firebase App
var firebaseApp *firebase.App
var firebaseDb *db.Client

// InitFirebase khởi tạo kết nối
func InitFirebase(credJSON []byte) {
	ctx := context.Background()
	opt := option.WithCredentialsJSON(credJSON)
	
	// 🔥 FIX: Thêm cấu hình Database URL (Lấy từ code Node.js cũ)
	conf := &firebase.Config{
		DatabaseURL: "https://hostduong-1991-default-rtdb.asia-southeast1.firebasedatabase.app",
	}

	app, err := firebase.NewApp(ctx, conf, opt)
	if err != nil {
		log.Fatalf("❌ Firebase Init Error: %v", err)
	}
	
	// Khởi tạo DB Client
	client, err := app.Database(ctx)
	if err != nil {
		log.Fatalf("❌ Firebase DB Error: %v", err)
	}
	
	firebaseApp = app
	firebaseDb = client
	fmt.Println("✅ Firebase initialized successfully.")
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

// CheckToken: Hàm kiểm tra chính
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

	// 3. Kiểm tra RAM (Lớp ưu tiên - Cache Hit)
	STATE.TokenMutex.RLock()
	cached, found := STATE.TokenCache[token]
	STATE.TokenMutex.RUnlock()

	if found {
		if now < cached.ExpiryTime {
			if cached.IsInvalid {
				return AuthResult{IsValid: false, Messenger: cached.Msg}
			}
			// ✅ Cache Hit
			return AuthResult{IsValid: true, SpreadsheetID: cached.Data.SpreadsheetID, Role: cached.Data.Role}
		}
		// Hết hạn -> Xóa để check lại Firebase
		STATE.TokenMutex.Lock()
		delete(STATE.TokenCache, token)
		STATE.TokenMutex.Unlock()
	}

	// 4. Kiểm tra Firebase (Lớp dự phòng - Cache Miss)
	ref := firebaseDb.NewRef("TOKEN_TIKTOK/" + token)
	var data map[string]interface{}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := ref.Get(ctx, &data); err != nil {
		// Log lỗi nhẹ để debug nhưng không chết server
		fmt.Printf("⚠️ Firebase Get Error: %v\n", err)
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Firebase"}
	}

	if data == nil {
		checkRateLimit(token, true)
		updateTokenCache(token, TokenData{}, true, "Token không tồn tại", 60000)
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	isExpired, _ := data["expired"].(bool)
	if !isExpired {
		checkRateLimit(token, true)
		updateTokenCache(token, TokenData{}, true, "Token lỗi", 60000)
		return AuthResult{IsValid: false, Messenger: "Token lỗi"}
	}

	// 5. Kiểm tra thời gian hết hạn
	expVal := data["expiration_time"]
	expTimeMs := parseExpirationTime(expVal)

	if expTimeMs == 0 || now > expTimeMs {
		updateTokenCache(token, TokenData{}, true, "Token hết hạn", 60000)
		return AuthResult{IsValid: false, Messenger: "Token hết hạn"}
	}

	// 6. Lấy SpreadsheetID và Role
	sid, _ := data["spreadsheetId"].(string)
	role, _ := data["role"].(string)

	tokenData := TokenData{
		SpreadsheetID: sid,
		Role:          role,
	}

	// 7. Ghi ngược vào RAM
	ttl := expTimeMs - now
	if ttl > CACHE.TOKEN_TTL_MS {
		ttl = CACHE.TOKEN_TTL_MS
	}
	
	updateTokenCache(token, tokenData, false, "", ttl)

	return AuthResult{IsValid: true, SpreadsheetID: sid, Role: role}
}

// Helper: Cập nhật Cache an toàn
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
// 🧠 LOGIC XỬ LÝ THỜI GIAN THÔNG MINH
// =================================================================================================
func parseExpirationTime(val interface{}) int64 {
	if val == nil {
		return 0
	}

	// Trường hợp 1: Dạng số
	if num, ok := val.(float64); ok {
		if num < 200000 { 
			// Excel Serial Date
			return int64((num - 25569) * 86400000) - (7 * 3600000)
		}
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

	normalized := strings.ReplaceAll(str, "-", "/")
	normalized = strings.ReplaceAll(normalized, ".", "/")
	
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	if loc == nil {
		loc = time.FixedZone("UTC+7", 7*60*60)
	}

	// Trường hợp 2: Chỉ có ngày (dd/mm/yyyy)
	if len(normalized) <= 10 {
		t, err := time.ParseInLocation("02/01/2006", normalized, loc)
		if err == nil {
			endOfDay := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, loc)
			return endOfDay.UnixMilli()
		}
	}

	// Trường hợp 3: Ngày giờ đầy đủ
	t, err := time.ParseInLocation("02/01/2006 15:04:05", normalized, loc)
	if err == nil {
		return t.UnixMilli()
	}

	// Trường hợp 4: ISO 8601
	tISO, err := time.Parse(time.RFC3339, str)
	if err == nil {
		return tISO.UnixMilli()
	}

	return 0
}

// =================================================================================================
// 🛡️ RATE LIMIT LOGIC
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

	if now-rec.LastReset > RATE.WINDOW_MS {
		rec.Count = 0
		rec.LastReset = now
	}

	if rec.BanUntil > 0 && now < rec.BanUntil {
		return false
	}

	rec.Count++
	if isError {
		rec.ErrorCount++
	}

	if rec.Count > RATE.TOKEN_MAX_REQ {
		return false
	}

	if rec.ErrorCount > RATE.MAX_ERROR {
		rec.BanUntil = now + RATE.BAN_MS
		return false
	}

	return true
}
