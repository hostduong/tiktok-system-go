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

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

var firebaseDB *db.Client
var AuthInitError error

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

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Kiểm tra Global Rate Limit (Lớp 0)
		if !CheckGlobalRateLimit() {
			http.Error(w, `{"status":"false","messenger":"Server Busy (Global Limit)"}`, 503)
			return
		}

		if firebaseDB == nil {
			http.Error(w, `{"status":"false","messenger":"Database Connecting..."}`, 503)
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

		tokenRaw, _ := bodyMap["token"].(string)
		tokenStr := strings.TrimSpace(tokenRaw) // Giữ nguyên hoa thường
		
		// 2. Kiểm tra Token + Cache (Lớp 1)
		authRes := CheckToken(tokenStr)
		if !authRes.IsValid {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": authRes.Messenger})
			return
		}

		// 3. Kiểm tra User Rate Limit (Lớp 2)
		if !CheckUserRateLimit(tokenStr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429) // Too Many Requests
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Spam detected (Rate Limit)"})
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
