package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// =================================================================================================
// 🔧 CẤU HÌNH TOKEN & RATE LIMIT (Sửa quy tắc tại đây)
// =================================================================================================

var TOKEN_RULES = struct {
	// --- LỚP 0 & 2: Rate Limit ---
	GLOBAL_MAX_REQ int   // Lớp 0: Max request toàn server / giây
	TOKEN_MAX_REQ  int   // Lớp 2: Max request mỗi token / giây
	WINDOW_MS      int64 // Cửa sổ thời gian (1 giây)

	// --- LỚP 1: Token Cache & Config ---
	MIN_LENGTH     int            // Độ dài tối thiểu
	REGEX          *regexp.Regexp // Định dạng cho phép
	CACHE_TTL_MS   int64          // Thời gian cache token đúng (1 giờ)
	BLOCK_TTL_MS   int64          // Thời gian block token sai (1 phút - Chống Spam)
}{
	GLOBAL_MAX_REQ: 1000,
	TOKEN_MAX_REQ:  5,
	WINDOW_MS:      1000,

	MIN_LENGTH:     10, // Để 10 cho dễ test, thực tế có thể là 50
	REGEX:          regexp.MustCompile(`^[a-zA-Z0-9]{10,200}$`),
	CACHE_TTL_MS:   3600000, // 60 phút
	BLOCK_TTL_MS:   60000,   // 60 giây
}

// =================================================================================================
// 🛡️ LOGIC KIỂM TRA 3 LỚP (CheckToken)
// =================================================================================================

// 🔥 LỚP 0: Hard Limit (Bảo vệ Server)
func CheckGlobalRateLimit() bool {
	STATE.GlobalCounter.Mutex.Lock()
	defer STATE.GlobalCounter.Mutex.Unlock()

	now := time.Now().UnixMilli()
	// Reset bộ đếm nếu qua giây mới
	if now-STATE.GlobalCounter.LastReset > TOKEN_RULES.WINDOW_MS {
		STATE.GlobalCounter.LastReset = now
		STATE.GlobalCounter.Count = 0
	}

	STATE.GlobalCounter.Count++
	return STATE.GlobalCounter.Count <= TOKEN_RULES.GLOBAL_MAX_REQ
}

// 🔥 LỚP 1: Check Token (RAM -> Firebase -> Negative Cache)
func CheckToken(token string) AuthResult {
	// 1. Validate định dạng cơ bản
	token = strings.TrimSpace(token)
	if token == "" || len(token) < TOKEN_RULES.MIN_LENGTH {
		return AuthResult{IsValid: false, Messenger: "Token không hợp lệ (Quá ngắn)"}
	}

	now := time.Now().UnixMilli()

	// 2. Kiểm tra Cache RAM
	STATE.TokenMutex.RLock()
	cached, exists := STATE.TokenCache[token]
	STATE.TokenMutex.RUnlock()

	if exists {
		// Nếu đang bị Block (Negative Cache)
		if cached.IsInvalid {
			if now < cached.ExpiryTime {
				return AuthResult{IsValid: false, Messenger: cached.Msg} // Chặn ngay
			}
			// Hết thời gian Block -> Xóa cache để check lại Firebase
			STATE.TokenMutex.Lock()
			delete(STATE.TokenCache, token)
			STATE.TokenMutex.Unlock()
		} else {
			// Token hợp lệ trong Cache
			if now < cached.ExpiryTime {
				return AuthResult{IsValid: true, SpreadsheetID: cached.Data.SpreadsheetID, Data: cached.Data.Data}
			}
			// Hết hạn Cache -> Xóa để check lại Firebase (cập nhật mới)
			STATE.TokenMutex.Lock()
			delete(STATE.TokenCache, token)
			STATE.TokenMutex.Unlock()
		}
	}

	// 3. Kiểm tra Firebase (Nếu Cache miss)
	if firebaseDB == nil {
		return AuthResult{IsValid: false, Messenger: "Database chưa sẵn sàng"}
	}

	var data map[string]interface{}
	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	if err := ref.Get(context.Background(), &data); err != nil {
		// Lỗi mạng -> Không Block (để user thử lại)
		log.Printf("❌ [FIREBASE ERROR] %v", err)
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Database"}
	}

	// 4. Xử lý kết quả & Ghi Cache
	STATE.TokenMutex.Lock()
	defer STATE.TokenMutex.Unlock()

	// Case: Token rác / Không tồn tại
	if data == nil {
		// 🔥 Tạo Negative Cache (Chống Spam)
		STATE.TokenCache[token] = &CachedToken{
			IsInvalid:  true,
			Msg:        "Token không tồn tại",
			ExpiryTime: now + TOKEN_RULES.BLOCK_TTL_MS,
		}
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	// Case: Token thiếu field
	if data["expired"] == nil || data["spreadsheetId"] == nil {
		STATE.TokenCache[token] = &CachedToken{
			IsInvalid:  true,
			Msg:        "Token lỗi data",
			ExpiryTime: now + TOKEN_RULES.BLOCK_TTL_MS,
		}
		return AuthResult{IsValid: false, Messenger: "Token lỗi data"}
	}

	// Case: Token hết hạn
	expStr := fmt.Sprintf("%v", data["expired"])
	expTime := parseExpirationTime(expStr)
	if time.Now().After(expTime) {
		STATE.TokenCache[token] = &CachedToken{
			IsInvalid:  true,
			Msg:        "Token hết hạn",
			ExpiryTime: now + TOKEN_RULES.BLOCK_TTL_MS,
		}
		return AuthResult{IsValid: false, Messenger: "Token hết hạn"}
	}

	// ✅ Case: Thành công -> Cache Positive
	sid := fmt.Sprintf("%v", data["spreadsheetId"])
	
	// Tính TTL thông minh (Min của Quy định hoặc Thời gian còn lại thực tế)
	ttl := TOKEN_RULES.CACHE_TTL_MS
	timeLeft := expTime.Sub(time.Now()).Milliseconds()
	if timeLeft < ttl {
		ttl = timeLeft
	}

	tokenData := TokenData{
		Token:         token,
		SpreadsheetID: sid,
		Data:          data,
		Expired:       expStr,
	}

	STATE.TokenCache[token] = &CachedToken{
		IsInvalid:  false,
		Data:       tokenData,
		ExpiryTime: now + ttl,
	}

	return AuthResult{IsValid: true, SpreadsheetID: sid, Data: data}
}

// 🔥 LỚP 2: Soft Limit (Công bằng cho User)
func CheckUserRateLimit(token string) bool {
	STATE.RateMutex.Lock()
	defer STATE.RateMutex.Unlock()

	now := time.Now().UnixMilli()
	rec, exists := STATE.RateLimit[token]

	if !exists {
		rec = &RateLimitData{LastReset: now, Count: 0}
		STATE.RateLimit[token] = rec
	}

	// Reset nếu qua giây mới
	if now-rec.LastReset > TOKEN_RULES.WINDOW_MS {
		rec.LastReset = now
		rec.Count = 0
	}

	rec.Count++
	return rec.Count <= TOKEN_RULES.TOKEN_MAX_REQ
}

// Helper: Parse ngày tháng
func parseExpirationTime(dateStr string) time.Time {
	layout := "02/01/2006"
	t, err := time.Parse(layout, dateStr)
	if err != nil {
		// Fallback 1 ngày (để an toàn)
		return time.Now().Add(24 * time.Hour)
	}
	return t.Add(23*time.Hour + 59*time.Minute)
}
