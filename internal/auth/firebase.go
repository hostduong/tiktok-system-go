package auth

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

// TokenData: Cấu trúc dữ liệu trả về từ Firebase DB
type TokenData struct {
	Expired       interface{} `json:"expired"` // Có thể là string hoặc số
	SpreadsheetID string      `json:"spreadsheetId"`
}

// CachedToken: Cấu trúc lưu trong RAM
type CachedToken struct {
	Data       *TokenData
	ExpiryTime time.Time
	IsValid    bool
	Message    string
}

// Authenticator: Bộ xác thực
type Authenticator struct {
	dbClient *db.Client
	
	// Cache Token trong RAM (Thay thế STATE.TOKEN_CACHE)
	tokenCache sync.Map // Map[string]*CachedToken
}

// Regex check format token (Giống Node.js)
var tokenRegex = regexp.MustCompile(`^[a-zA-Z0-9]{50,200}$`)

// NewAuthenticator khởi tạo kết nối Firebase
func NewAuthenticator() (*Authenticator, error) {
	ctx := context.Background()
	credsJSON := os.Getenv("FIREBASE_CREDENTIALS")
	if credsJSON == "" {
		return nil, fmt.Errorf("missing env var: FIREBASE_CREDENTIALS")
	}

	// Config Firebase
	conf := &firebase.Config{
		DatabaseURL: "https://hostduong-1991-default-rtdb.asia-southeast1.firebasedatabase.app",
	}

	// Khởi tạo App
	app, err := firebase.NewApp(ctx, conf, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return nil, fmt.Errorf("firebase init error: %v", err)
	}

	// Kết nối Realtime Database
	client, err := app.Database(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase db error: %v", err)
	}

	return &Authenticator{
		dbClient: client,
	}, nil
}

// VerifyToken: Kiểm tra tính hợp lệ của Token (Cache -> DB)
func (a *Authenticator) VerifyToken(token string) (bool, *TokenData, string) {
	// 1. Validate Format
	if !tokenRegex.MatchString(token) {
		return false, nil, "Token sai định dạng"
	}

	// 2. Check Cache RAM
	if val, ok := a.tokenCache.Load(token); ok {
		cached := val.(*CachedToken)
		if time.Now().Before(cached.ExpiryTime) {
			if !cached.IsValid {
				return false, nil, cached.Message
			}
			return true, cached.Data, "OK"
		}
		// Hết hạn cache -> Xóa để check lại
		a.tokenCache.Delete(token)
	}

	// 3. Check Firebase Realtime Database
	ref := a.dbClient.NewRef("TOKEN_TIKTOK/" + token)
	var data TokenData
	if err := ref.Get(context.Background(), &data); err != nil {
		// Lỗi kết nối hoặc không tìm thấy
		a.cacheResult(token, nil, false, "Lỗi kết nối hoặc không tìm thấy token", 1*time.Minute)
		return false, nil, "Lỗi kết nối Firebase"
	}

	// 4. Validate Data
	if data.SpreadsheetID == "" {
		a.cacheResult(token, nil, false, "Token không tồn tại hoặc thiếu ID", 1*time.Minute)
		return false, nil, "Token không tồn tại"
	}

	// 5. Check Expired Time
	expTime := parseTime(data.Expired)
	now := time.Now()
	if expTime.IsZero() || now.After(expTime) {
		a.cacheResult(token, nil, false, "Token hết hạn", 1*time.Minute)
		return false, nil, "Token hết hạn"
	}

	// 6. Thành công -> Cache 1 giờ (Hoặc đến lúc hết hạn)
	ttl := 1 * time.Hour
	if sub := expTime.Sub(now); sub < ttl {
		ttl = sub
	}
	a.cacheResult(token, &data, true, "OK", ttl)

	return true, &data, "OK"
}

// ClearCache xóa cache của 1 token (Dùng khi create_sheets update ID mới)
func (a *Authenticator) ClearCache(token string) {
	a.tokenCache.Delete(token)
}

// cacheResult lưu kết quả vào RAM
func (a *Authenticator) cacheResult(token string, data *TokenData, isValid bool, msg string, ttl time.Duration) {
	a.tokenCache.Store(token, &CachedToken{
		Data:       data,
		IsValid:    isValid,
		Message:    msg,
		ExpiryTime: time.Now().Add(ttl),
	})
}

// parseTime chuyển đổi thời gian từ Excel/String sang Time (Giống Node.js Utils)
func parseTime(v interface{}) time.Time {
	if v == nil {
		return time.Time{}
	}

	// Trường hợp 1: Số (Excel Serial Date)
	if valNum, ok := v.(float64); ok {
		// (v - 25569) * 86400000 ... logic Excel to Unix
		unixDays := valNum - 25569
		unixSec := int64(unixDays * 86400)
		return time.Unix(unixSec, 0).Add(-7 * time.Hour) // Trừ múi giờ nếu cần khớp Node.js logic
	}

	// Trường hợp 2: String "dd/mm/yyyy"
	valStr := fmt.Sprintf("%v", v)
	parts := strings.Split(valStr, "/")
	if len(parts) >= 3 {
		d, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		m, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		y, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
		// Go dùng layout cố định: "2006-01-02"
		t := time.Date(y, time.Month(m), d, 23, 59, 59, 0, time.Local)
		return t
	}

	return time.Time{}
}
