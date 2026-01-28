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

// InitAuthService: Khởi tạo Firebase
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

// AuthMiddleware: Middleware kiểm tra token
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		tokenStr := CleanString(bodyMap["token"])
		
		// 🔥 Gọi hàm CheckToken (Viết hoa)
		authRes := CheckToken(tokenStr)
		if !authRes.IsValid {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": authRes.Messenger})
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

// 🔥 ĐỔI TÊN HÀM: checkTokenFirebase -> CheckToken (Exported)
// Để các file handler_log.go, handler_search.go có thể gọi được
func CheckToken(token string) AuthResult {
	if token == "" || len(token) < 50 {
		return AuthResult{IsValid: false, Messenger: "Token không hợp lệ"}
	}

	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	var data map[string]interface{}
	if err := ref.Get(context.Background(), &data); err != nil || data == nil {
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	if data["expired"] == nil || data["spreadsheetId"] == nil {
		return AuthResult{IsValid: false, Messenger: "Token lỗi data"}
	}

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
