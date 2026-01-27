package auth

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

type Authenticator struct {
	client *db.Client
}

type TokenData struct {
	Expired       interface{} `json:"expired"`
	SpreadsheetID string      `json:"spreadsheetId"`
	Email         string      `json:"email"`
	UID           string      `json:"uid"`
}

func NewAuthenticator() (*Authenticator, error) {
	ctx := context.Background()
	credJSON := os.Getenv("FIREBASE_CREDENTIALS")
	
	opts := []option.ClientOption{}
	if credJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(credJSON)))
	}

	conf := &firebase.Config{
		DatabaseURL: "https://hostduong-1991-default-rtdb.asia-southeast1.firebasedatabase.app",
	}

	app, err := firebase.NewApp(ctx, conf, opts...)
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Firebase: %v", err)
	}

	client, err := app.Database(ctx)
	if err != nil {
		return nil, fmt.Errorf("lỗi kết nối Database: %v", err)
	}

	return &Authenticator{client: client}, nil
}

func (a *Authenticator) VerifyToken(token string) (bool, *TokenData, string) {
	if len(token) < 5 {
		return false, nil, "Token quá ngắn"
	}

	ctx := context.Background()
	ref := a.client.NewRef("TOKEN_TIKTOK/" + token)
	var data TokenData
	
	if err := ref.Get(ctx, &data); err != nil {
		log.Printf("Lỗi đọc DB: %v", err)
		return false, nil, "Lỗi kết nối Server"
	}

	if data.SpreadsheetID == "" {
		return false, nil, "Token không tồn tại"
	}

	// Xử lý hạn sử dụng ĐA NĂNG
	expireTime := parseExpiration(data.Expired)
	
	// Debug log: in ra để bạn kiểm tra server hiểu ngày thế nào
	// log.Printf("Token Check: Input=%v -> Parsed=%d (Now=%d)", data.Expired, expireTime, time.Now().UnixMilli())

	if expireTime == 0 {
		return false, nil, "Lỗi định dạng ngày hết hạn"
	}

	if time.Now().UnixMilli() > expireTime {
		return false, nil, "Token đã hết hạn sử dụng"
	}

	return true, &data, "OK"
}

// ---------------------------------------------------------
// 🔥 BỘ XỬ LÝ THỜI GIAN ĐA NĂNG (UNIVERSAL PARSER)
// ---------------------------------------------------------
func parseExpiration(val interface{}) int64 {
	if val == nil {
		return 0
	}

	// TRƯỜNG HỢP 1: DẠNG SỐ (Excel Date hoặc Timestamp)
	if v, ok := val.(float64); ok {
		// Nếu số nhỏ (< 100,000) -> Excel Serial Date (Ví dụ: 45678)
		if v < 100000.0 {
			return int64((v - 25569) * 86400000) - (7 * 3600000)
		}
		// Nếu số lớn -> Timestamp (Milliseconds)
		return int64(v)
	}

	// TRƯỜNG HỢP 2: DẠNG CHUỖI
	if s, ok := val.(string); ok {
		s = strings.TrimSpace(s)

		// 2.1: Thử parse chuẩn ISO 8601 (2025-01-27T10:00:00Z)
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UnixMilli()
		}

		// 2.2: Parse định dạng Việt Nam (dd/mm/yyyy) linh hoạt
		// Tách chuỗi bằng bất kỳ ký tự nào: / - : khoảng trắng
		parts := strings.FieldsFunc(s, func(r rune) bool {
			return r == '/' || r == '-' || r == ':' || r == ' '
		})

		if len(parts) >= 3 {
			// Thứ tự chuẩn: Ngày - Tháng - Năm
			d, _ := strconv.Atoi(parts[0])
			m, _ := strconv.Atoi(parts[1])
			y, _ := strconv.Atoi(parts[2])

			// Mặc định: Cuối ngày (23:59:59)
			h, min, sec := 23, 59, 59

			// Nếu có giờ phút giây -> Ghi đè
			if len(parts) >= 4 { h, _ = strconv.Atoi(parts[3]) }
			if len(parts) >= 5 { min, _ = strconv.Atoi(parts[4]) }
			if len(parts) >= 6 { sec, _ = strconv.Atoi(parts[5]) }

			// Tạo thời gian UTC giả định
			t := time.Date(y, time.Month(m), d, h, min, sec, 0, time.UTC)

			// Trừ 7 tiếng (25200000 ms) để đưa về đúng mốc thời gian VN
			// (Vì server hiểu t là UTC, nhưng thực tế input là GMT+7)
			return t.UnixMilli() - 25200000
		}
	}

	return 0
}
