package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

var firebaseDB *db.Client
var AuthInitError error

// InitAuthService: Khởi tạo Firebase
func InitAuthService(credJSON []byte) {
	if len(credJSON) == 0 {
		AuthInitError = fmt.Errorf("Credential Data is empty")
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	ctx := context.Background()
	opt := option.WithCredentialsJSON(credJSON)
	
	// URL này lúc chiều chạy được, giữ nguyên
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
	fmt.Println("✅ Firebase Service initialized (V4).")
}

// AuthMiddleware: Middleware kiểm tra token
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if AuthInitError != nil {
			http.Error(w, `{"status":"false","messenger":"Server Config Error: `+AuthInitError.Error()+`"}`, 500)
			return
		}
		if firebaseDB == nil {
			http.Error(w, `{"status":"false","messenger":"Database Connecting... Try again."}`, 503)
			return
		}

		// Đọc Body
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"status":"false","messenger":"Read Body Error"}`, 400)
			return
		}
		
		// Trả lại Body cho Handler sau
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var bodyMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
			http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
			return
		}

		tokenStr := CleanString(bodyMap["token"])
		
		// Gọi hàm CheckToken
		authRes := CheckToken(tokenStr)
		if !authRes.IsValid {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": authRes.Messenger})
			return
		}

		// Lưu vào Context
		ctx := context.WithValue(r.Context(), "tokenData", &TokenData{
			Token:         tokenStr,
			SpreadsheetID: authRes.SpreadsheetID,
			Data:          authRes.Data,
		})

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CheckToken: Logic kiểm tra Token (QUAY VỀ BẢN CHUẨN)
func CheckToken(token string) AuthResult {
	if firebaseDB == nil {
		return AuthResult{IsValid: false, Messenger: "Database chưa sẵn sàng"}
	}

	if token == "" || len(token) < 10 {
		return AuthResult{IsValid: false, Messenger: "Token không hợp lệ"}
	}

	// 🔥 QUAN TRỌNG: Đọc về map[string]interface{} thay vì Struct cứng
	// Điều này giúp code linh hoạt với mọi kiểu dữ liệu JSON trả về
	var data map[string]interface{}
	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	
	if err := ref.Get(context.Background(), &data); err != nil {
		log.Printf("❌ Firebase Error: %v", err)
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Database"}
	}

	if data == nil {
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	// Kiểm tra các trường bắt buộc
	if data["expired"] == nil || data["spreadsheetId"] == nil {
		return AuthResult{IsValid: false, Messenger: "Token lỗi data (Thiếu expired/spreadsheetId)"}
	}

	// Xử lý ngày hết hạn
	expStr := fmt.Sprintf("%v", data["expired"])
	expTime := parseExpirationTime(expStr)
	
	// Debug Log nhẹ để kiểm tra
	// log.Printf("Token Check: %s | Exp: %v | ID: %v", token[:10]+"...", expTime, data["spreadsheetId"])

	if time.Now().After(expTime) {
		return AuthResult{IsValid: false, Messenger: "Token hết hạn"}
	}

	sid := fmt.Sprintf("%v", data["spreadsheetId"])
	return AuthResult{IsValid: true, SpreadsheetID: sid, Data: data}
}

func parseExpirationTime(dateStr string) time.Time {
	layout := "02/01/2006"
	t, err := time.Parse(layout, dateStr)
	if err != nil {
		// Fallback 1 ngày nếu lỗi format (để tránh chặn sai)
		return time.Now().Add(24 * time.Hour)
	}
	return t.Add(23*time.Hour + 59*time.Minute)
}
