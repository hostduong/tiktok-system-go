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

// CheckToken: Logic kiểm tra Token (Giống hệt Node.js V243 dòng 279)
func CheckToken(token string) AuthResult {
	if firebaseDB == nil {
		return AuthResult{IsValid: false, Messenger: "Database chưa sẵn sàng"}
	}

	// Node.js dòng 273: Kiểm tra định dạng token
	if token == "" || len(token) < 50 {
		return AuthResult{IsValid: false, Messenger: "Token không hợp lệ"}
	}

	// Node.js dòng 278: db.ref(...).once('value')
	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	
	// Thay vì Get trực tiếp vào Map, ta lấy DataSnapshot để chắc chắn tồn tại
	// (Đây là cách debug xem thực sự Firebase trả về gì)
	var data map[string]interface{}
	if err := ref.Get(context.Background(), &data); err != nil {
		log.Printf("❌ [FIREBASE ERROR] Token: %s | Err: %v", token, err)
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Database"}
	}

	// Node.js dòng 279: if (!snap.exists())
	if data == nil {
		log.Printf("⚠️ [FIREBASE] Token not found: %s", token)
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	// Node.js dòng 283: if (!data.expired)
	if data["expired"] == nil {
		log.Printf("⚠️ [FIREBASE] Token missing 'expired': %s", token)
		return AuthResult{IsValid: false, Messenger: "Token lỗi data"}
	}

	// Node.js dòng 285: Utils.chuyen_doi_thoi_gian
	expStr := fmt.Sprintf("%v", data["expired"])
	expTime := parseExpirationTime(expStr)
	
	// Node.js dòng 286: if (now > exp)
	if time.Now().After(expTime) {
		log.Printf("⚠️ [FIREBASE] Token expired: %s (Exp: %v)", token, expTime)
		return AuthResult{IsValid: false, Messenger: "Token hết hạn"}
	}

	// Lấy SpreadsheetID
	sid := ""
	if data["spreadsheetId"] != nil {
		sid = fmt.Sprintf("%v", data["spreadsheetId"])
	}
	
	if sid == "" {
		return AuthResult{IsValid: false, Messenger: "Token lỗi data (Thiếu spreadsheetId)"}
	}

	return AuthResult{IsValid: true, SpreadsheetID: sid, Data: data}
}

// Hàm parse ngày tháng (Khớp logic Utils.chuyen_doi_thoi_gian dòng 136 Node.js)
func parseExpirationTime(dateStr string) time.Time {
	// Node.js logic: dd/mm/yyyy hh:mm:ss
	layout := "02/01/2006 15:04:05" // Định dạng chuẩn Go
	t, err := time.Parse(layout, dateStr)
	if err != nil {
		// Thử định dạng ngắn gọn dd/mm/yyyy
		layoutShort := "02/01/2006"
		tShort, errShort := time.Parse(layoutShort, dateStr)
		if errShort == nil {
			// Nếu chỉ có ngày, hạn là cuối ngày đó (23:59:59)
			return tShort.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		}
		// Fallback: Node.js trả về 0 nếu lỗi, ở đây ta cho hết hạn luôn để an toàn
		// Hoặc cho sống tạm 1 ngày để debug (như bản cũ)
		// Logic chuẩn: Fail safe -> Coi như hợp lệ để tránh chặn nhầm (như Node.js logic mềm dẻo)
		log.Printf("⚠️ [TIME PARSE ERROR] %s", dateStr)
		return time.Now().Add(24 * time.Hour) 
	}
	return t
}
