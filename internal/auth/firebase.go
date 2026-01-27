package auth

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

type Authenticator struct {
	Client        *auth.Client
	SpreadsheetID string // Có thể rỗng nếu dùng multi-user dynamic
}

func NewAuthenticator() (*Authenticator, error) {
	ctx := context.Background()

	// 1. Lấy Key từ biến môi trường
	credJSON := os.Getenv("FIREBASE_CREDENTIALS")
	
	// Check sơ bộ xem có key không
	if credJSON == "" {
		return nil, fmt.Errorf("biến môi trường FIREBASE_CREDENTIALS đang rỗng")
	}

	// 2. Nạp Key trực tiếp từ chuỗi JSON (Không tìm file trên đĩa)
	opt := option.WithCredentialsJSON([]byte(credJSON))
	
	// 3. Khởi tạo App
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Firebase App: %v", err)
	}

	// 4. Khởi tạo Auth Client (Dùng để verify token của user)
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Auth Client: %v", err)
	}

	// Logic lấy SpreadsheetID mặc định (nếu có trong key JSON - tùy chọn)
	// Ta tạm để rỗng vì logic V300 lấy sheet ID từ token user gửi lên
	sid := "" 
	if strings.Contains(credJSON, "project_id") {
		// Log chơi thôi chứ không quan trọng
		log.Println("✅ Đã nạp Firebase Credentials thành công.")
	}

	return &Authenticator{
		Client:        client,
		SpreadsheetID: sid,
	}, nil
}

// VerifyToken: Hàm xác thực token user gửi lên
func (a *Authenticator) VerifyToken(idToken string) (bool, *TokenData, string) {
	token, err := a.Client.VerifyIDToken(context.Background(), idToken)
	if err != nil {
		// Token hết hạn hoặc fake
		return false, nil, fmt.Sprintf("Token lỗi: %v", err)
	}

	// Lấy claims (thông tin trong token)
	claims := token.Claims
	
	// Lấy spreadsheet_id từ claims (User V300 thường lưu sheet_id trong token)
	sid := ""
	if val, ok := claims["spreadsheet_id"]; ok {
		sid = fmt.Sprintf("%v", val)
	}

	// Nếu không có trong token, fallback về logic cũ hoặc trả về rỗng
	if sid == "" {
		// Logic cũ Node.js: Có thể hardcode hoặc lấy từ DB
		// Ở đây ta tạm trả về rỗng, Handler sẽ xử lý sau
	}

	return true, &TokenData{
		UID:           token.UID,
		Email:         fmt.Sprintf("%v", claims["email"]),
		SpreadsheetID: sid,
	}, "OK"
}

type TokenData struct {
	UID           string
	Email         string
	SpreadsheetID string
}
