package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

var firebaseDB *db.Client

// Struct lưu thông tin Token sau khi decode
type TokenData struct {
	Token         string
	SpreadsheetID string
	Data          map[string]interface{}
}

// Struct trả về kết quả check token
type AuthResult struct {
	IsValid       bool
	Messenger     string
	SpreadsheetID string
	Data          map[string]interface{}
}

// 🔥 Đổi tên hàm thành InitAuthService cho khớp với main.go
func InitAuthService(credJSON []byte) {
	ctx := context.Background()
	opt := option.WithCredentialsJSON(credJSON)
	
	conf := &firebase.Config{
		DatabaseURL: "https://hostduong-1991-default-rtdb.asia-southeast1.firebasedatabase.app",
	}

	app, err := firebase.NewApp(ctx, conf, opt)
	if err != nil {
		log.Fatalf("❌ [CRITICAL] Firebase Init Error: %v", err)
	}

	client, err := app.Database(ctx)
	if err != nil {
		log.Fatalf("❌ [CRITICAL] Firebase DB Error: %v", err)
	}

	firebaseDB = client
	fmt.Println("✅ Firebase Service initialized (V4).")
}

// 🔥 AuthMiddleware: Kiểm tra token trước khi vào Handler
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Đọc Body để lấy Token (Copy body ra để không mất stream)
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"status":"false","messenger":"Read Body Error"}`, 400)
			return
		}
		
		// Khôi phục Body để Handler phía sau đọc lại được
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var bodyMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
			// Nếu JSON lỗi, vẫn cho qua để Handler sau xử lý hoặc chặn tùy logic, 
			// nhưng ở đây ta chặn luôn cho an toàn.
			http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
			return
		}

		tokenStr := CleanString(bodyMap["token"])
		
		// 2. Check Token với Firebase
		authRes := checkTokenFirebase(tokenStr)
		if !authRes.IsValid {
			w.Header().Set("Content-Type", "application/json")
			// Trả về 200 OK nhưng nội dung báo lỗi (theo phong cách Node.js cũ)
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": authRes.Messenger})
			return
		}

		// 3. Lưu thông tin vào Context
		ctx := context.WithValue(r.Context(), "tokenData", &TokenData{
			Token:         tokenStr,
			SpreadsheetID: authRes.SpreadsheetID,
			Data:          authRes.Data,
		})

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logic check token chi tiết
func checkTokenFirebase(token string) AuthResult {
	if token == "" || len(token) < 50 {
		return AuthResult{IsValid: false, Messenger: "Token không hợp lệ"}
	}

	// Check Firebase DB
	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	var data map[string]interface{}
	if err := ref.Get(context.Background(), &data); err != nil || data == nil {
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	if data["expired"] == nil || data["spreadsheetId"] == nil {
		return AuthResult{IsValid: false, Messenger: "Token lỗi data"}
	}

	// Check ngày hết hạn
	expStr := fmt.Sprintf("%v", data["expired"])
	expTime := parseExpirationTime(expStr)
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
		return time.Now().Add(24 * time.Hour)
	}
	return t.Add(23*time.Hour + 59*time.Minute)
}
