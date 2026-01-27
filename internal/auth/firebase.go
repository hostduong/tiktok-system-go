package auth

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

// Authenticator quản lý kết nối Firebase Database
type Authenticator struct {
	client *db.Client
}

// TokenData mapping dữ liệu từ Firebase Database
type TokenData struct {
	Expired       interface{} `json:"expired"`       // Hạn sử dụng (số hoặc chuỗi)
	SpreadsheetID string      `json:"spreadsheetId"` // ID file Excel
	Email         string      `json:"email"`
	UID           string      `json:"uid"`
}

// NewAuthenticator khởi tạo kết nối (Thay thế NewService cũ)
func NewAuthenticator() (*Authenticator, error) {
	ctx := context.Background()
	credJSON := os.Getenv("FIREBASE_CREDENTIALS")
	
	opts := []option.ClientOption{}
	if credJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(credJSON)))
	}

	// ⚠️ CẤU HÌNH DATABASE URL (Lấy từ code Node.js cũ)
	conf := &firebase.Config{
		DatabaseURL: "https://hostduong-1991-default-rtdb.asia-southeast1.firebasedatabase.app",
	}

	app, err := firebase.NewApp(ctx, conf, opts...)
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Firebase App: %v", err)
	}

	// Kết nối Realtime Database
	client, err := app.Database(ctx)
	if err != nil {
		return nil, fmt.Errorf("lỗi kết nối Database: %v", err)
	}

	return &Authenticator{client: client}, nil
}

// VerifyToken: Kiểm tra License Key trong Database
// (Thay thế logic check JWT cũ)
func (a *Authenticator) VerifyToken(token string) (bool, *TokenData, string) {
	// 1. Validate độ dài (Tránh spam)
	if token == "" || len(token) < 5 {
		return false, nil, "Token không hợp lệ (Quá ngắn)"
	}

	ctx := context.Background()
	
	// 2. Query Database: TOKEN_TIKTOK/{token}
	ref := a.client.NewRef("TOKEN_TIKTOK/" + token)
	var data TokenData
	
	if err := ref.Get(ctx, &data); err != nil {
		log.Printf("Lỗi đọc DB cho token %s: %v", token, err)
		return false, nil, "Lỗi kết nối hoặc Token không tồn tại"
	}

	// 3. Kiểm tra dữ liệu (Nếu không có SpreadsheetID tức là token rác)
	if data.SpreadsheetID == "" {
		return false, nil, "Token không tồn tại trong hệ thống"
	}

	// 4. Xử lý thời gian hết hạn (Logic giống Node.js: chuyen_doi_thoi_gian)
	expireTime := parseExpiration(data.Expired)
	
	if expireTime == 0 {
		return false, nil, "Lỗi định dạng ngày hết hạn trên Server"
	}

	// So sánh thời gian
	if time.Now().UnixMilli() > expireTime {
		return false, nil, "Token đã hết hạn sử dụng"
	}

	// Thành công!
	return true, &data, "OK"
}

// Hàm phụ trợ: Parse ngày tháng (Hỗ trợ cả số Excel và chuỗi dd/mm/yyyy)
func parseExpiration(val interface{}) int64 {
	if val == nil {
		return 0
	}

	// Case 1: Dạng số (Excel Serial Date) - Ví dụ: 45285
	if v, ok := val.(float64); ok {
		// Công thức chuyển Excel Date sang Unix Millis
		return int64((v - 25569) * 86400000) - (7 * 3600000)
	}

	// Case 2: Dạng chuỗi "25/12/2025"
	if s, ok := val.(string); ok {
		s = strings.TrimSpace(s)
		parts := strings.Split(s, "/")
		if len(parts) >= 3 {
			d, _ := strconv.Atoi(parts[0])
			m, _ := strconv.Atoi(parts[1])
			y, _ := strconv.Atoi(parts[2])
			
			// Tạo thời gian UTC (giống Node.js Date.UTC)
			t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
			// Trừ đi 7 tiếng (25200000ms) để khớp múi giờ VN như code cũ
			return t.UnixMilli() - 25200000
		}
	}

	return 0
}
