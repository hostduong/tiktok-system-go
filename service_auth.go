package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

// Biến toàn cục lưu kết nối Database và lỗi khởi tạo
var firebaseDB *db.Client
var AuthInitError error

// =================================================================================================
// 🔧 CẤU HÌNH TOKEN & RATE LIMIT (Centralized Config)
// =================================================================================================

var TOKEN_RULES = struct {
	GLOBAL_MAX_REQ int   // Giới hạn request toàn server / giây
	TOKEN_MAX_REQ  int   // Giới hạn request mỗi token / giây
	WINDOW_MS      int64 // Cửa sổ thời gian tính rate limit (ms)
	MIN_LENGTH     int   // Độ dài tối thiểu của token
	CACHE_TTL_MS   int64 // Thời gian lưu cache RAM (60 phút)
	BLOCK_TTL_MS   int64 // Thời gian chặn token sai (1 phút)
}{
	GLOBAL_MAX_REQ: 1000,
	TOKEN_MAX_REQ:  5,
	WINDOW_MS:      1000,
	MIN_LENGTH:     10,
	CACHE_TTL_MS:   3600000, // 1 giờ
	BLOCK_TTL_MS:   60000,   // 60 giây
}

// =================================================================================================
// 🚀 PHẦN 1: KHỞI TẠO & MIDDLEWARE (Xử lý Request)
// =================================================================================================

// InitAuthService: Khởi tạo kết nối đến Firebase
func InitAuthService(credJSON []byte) {
	// Kiểm tra nếu không có key JSON
	if len(credJSON) == 0 {
		AuthInitError = fmt.Errorf("Dữ liệu Credential bị trống")
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	ctx := context.Background()
	opt := option.WithCredentialsJSON(credJSON)
	
	// Cấu hình URL Database (Phải chuẩn theo Firebase Console)
	conf := &firebase.Config{
		DatabaseURL: "https://hostduong-1991-default-rtdb.asia-southeast1.firebasedatabase.app",
	}

	// Tạo App Firebase
	app, err := firebase.NewApp(ctx, conf, opt)
	if err != nil {
		AuthInitError = fmt.Errorf("Lỗi khởi tạo Firebase App: %v", err)
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	// Tạo Client Database
	client, err := app.Database(ctx)
	if err != nil {
		AuthInitError = fmt.Errorf("Lỗi kết nối Database: %v", err)
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	// Gán vào biến toàn cục để dùng sau này
	firebaseDB = client
	fmt.Println("✅ Firebase Service initialized (V4) - Standard Response Edition.")
}

// AuthMiddleware: Cánh cổng bảo vệ, kiểm tra Token trước khi vào Controller
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. [LỚP 0] Global Rate Limit (Chặn tấn công DDoS)
		if !CheckGlobalRateLimit() {
			http.Error(w, `{"status":"false","messenger":"Server Busy (Global Limit)"}`, 503)
			return
		}

		// Kiểm tra kết nối DB
		if firebaseDB == nil {
			http.Error(w, `{"status":"false","messenger":"Lỗi kết nối Database"}`, 503)
			return
		}

		// Đọc Body request một cách an toàn (để có thể đọc lại sau này)
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"status":"false","messenger":"Read Body Error"}`, 400)
			return
		}
		// Trả lại body cho request để các hàm sau có thể đọc tiếp
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Parse JSON để lấy Token
		var bodyMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
			http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
			return
		}

		// Lấy chuỗi Token và xóa khoảng trắng thừa
		tokenRaw, _ := bodyMap["token"].(string)
		tokenStr := strings.TrimSpace(tokenRaw)
		
		// 2. [LỚP 1] Check Token (Quy trình: RAM -> Firebase -> Cache)
		authRes := CheckToken(tokenStr)
		
		// 🔥 XỬ LÝ LỖI CHUẨN FORM (Error vs False)
		if !authRes.IsValid {
			w.Header().Set("Content-Type", "application/json")
			
			// Mặc định là lỗi nghiệp vụ (false)
			status := "false"
			
			// Các trường hợp lỗi cấu trúc/dữ liệu thì trả về "error"
			switch authRes.Messenger {
			case "Token hết hạn hoặc không tồn tại":
				status = "error"
			case "Không có spreadsheetsId":
				status = "error"
			case "Token không hợp lệ": // Trường hợp token quá ngắn/rỗng
				status = "error"
			}

			// Trả về JSON chuẩn theo yêu cầu
			json.NewEncoder(w).Encode(map[string]string{
				"status":    status,
				"messenger": authRes.Messenger,
			})
			return
		}

		// 3. [LỚP 2] User Rate Limit (Chống spam từng user)
		if !CheckUserRateLimit(tokenStr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429) // Mã lỗi Too Many Requests
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Spam detected (Rate Limit)"})
			return
		}

		// Token hợp lệ -> Lưu thông tin vào Context để các hàm sau dùng
		ctx := context.WithValue(r.Context(), "tokenData", &TokenData{
			Token:         tokenStr,
			SpreadsheetID: authRes.SpreadsheetID,
			Data:          authRes.Data,
		})

		// Chuyển tiếp sang hàm xử lý chính
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// =================================================================================================
// 🛡️ PHẦN 2: LOGIC CHECK TOKEN & TIME PARSER (NGHIỆP VỤ CỐT LÕI)
// =================================================================================================

// CheckToken: Hàm kiểm tra tính hợp lệ của Token
func CheckToken(token string) AuthResult {
	// 1. Kiểm tra định dạng cơ bản (Rỗng hoặc quá ngắn)
	if token == "" || len(token) < TOKEN_RULES.MIN_LENGTH {
		// Trả về message khớp với case 1: "Token hết hạn hoặc không tồn tại" (hoặc sai định dạng)
		// Theo yêu cầu của bạn "token sai hoặc không có", ta gom chung vào message này
		return AuthResult{IsValid: false, Messenger: "Token hết hạn hoặc không tồn tại"}
	}

	now := time.Now().UnixMilli()

	// 2. Kiểm tra trong Cache RAM trước (Tốc độ cao)
	STATE.TokenMutex.RLock()
	cached, exists := STATE.TokenCache[token]
	STATE.TokenMutex.RUnlock()

	if exists {
		// Nếu là Cache chặn (Token rác/sai đã bị nhớ trước đó)
		if cached.IsInvalid {
			if now < cached.ExpiryTime {
				return AuthResult{IsValid: false, Messenger: cached.Msg}
			}
			// Hết thời gian phạt -> Xóa cache để check lại Firebase
			deleteTokenCache(token)
		} else {
			// Cache hợp lệ (Token đúng đã nhớ)
			if now < cached.ExpiryTime {
				return AuthResult{IsValid: true, SpreadsheetID: cached.Data.SpreadsheetID, Data: cached.Data.Data}
			}
			// Hết hạn cache -> Xóa để lấy thông tin mới nhất từ Firebase
			deleteTokenCache(token)
		}
	}

	// 3. Kiểm tra Firebase (Nếu không có trong Cache)
	if firebaseDB == nil {
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Database"}
	}

	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	var data map[string]interface{}
	
	// Gọi Firebase
	if err := ref.Get(context.Background(), &data); err != nil {
		log.Printf("❌ [FIREBASE ERROR] %v", err)
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Database"}
	}

	// CASE 1: Token không tồn tại trong DB -> Cache chặn
	if data == nil {
		setCache(token, nil, true, "Token hết hạn hoặc không tồn tại", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token hết hạn hoặc không tồn tại"}
	}

	// CASE 4: Token tồn tại nhưng thiếu dữ liệu quan trọng -> Cache chặn
	if data["expired"] == nil {
		setCache(token, nil, true, "Token lỗi data (Thiếu expired)", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token lỗi data"}
	}
	if data["spreadsheetId"] == nil {
		// Yêu cầu: status error, msg: Không có spreadsheetsId
		setCache(token, nil, true, "Không có spreadsheetsId", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Không có spreadsheetsId"}
	}

	// CASE 2: Kiểm tra Hạn sử dụng (Smart Time Parse)
	expStr := fmt.Sprintf("%v", data["expired"])
	expTime := parseSmartTime(expStr)
	
	timeLeft := expTime.Sub(time.Now()).Milliseconds()

	// Nếu parse lỗi hoặc thời gian còn lại <= 0
	if expTime.IsZero() || timeLeft <= 0 {
		setCache(token, nil, true, "Token đã hết hạn", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token đã hết hạn"}
	}

	// CASE: Thành công (Token ngon) -> Cache Positive
	sid := fmt.Sprintf("%v", data["spreadsheetId"])
	
	// Logic TTL Cache: Cache sống bằng thời gian còn lại của Token, nhưng không quá 1 giờ
	ttl := TOKEN_RULES.CACHE_TTL_MS
	if ttl > timeLeft {
		ttl = timeLeft
	}

	validData := TokenData{
		Token:         token,
		SpreadsheetID: sid,
		Data:          data,
		Expired:       expStr,
	}
	setCache(token, &validData, false, "", ttl)

	return AuthResult{IsValid: true, SpreadsheetID: sid, Data: data}
}

// Hàm parse thời gian thông minh (Hỗ trợ số, ISO, Date Only)
func parseSmartTime(dateStr string) time.Time {
	// Múi giờ Việt Nam cứng (+7)
	vnZone := time.FixedZone("UTC+7", 7*3600)
	s := strings.TrimSpace(dateStr)

	// 1. Kiểm tra dạng số (Timestamp)
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		// Nếu > 10^11 thì là milliseconds (vì 10^11 giây là năm 5138)
		if ts > 100000000000 { 
			return time.UnixMilli(ts).In(vnZone)
		}
		return time.Unix(ts, 0).In(vnZone)
	}

	// 2. Kiểm tra chuẩn ISO-8601 / RFC3339 (Có sẵn múi giờ hoặc UTC)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(vnZone)
	}
	// Fallback cho ISO thiếu Timezone: 2026-01-29T06:03:55 -> Gán VN Zone
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, vnZone); err == nil {
		return t
	}

	// 3. Xử lý Date-Only (Chỉ có ngày -> Chuyển thành cuối ngày 23:59:59)
	// Điều kiện: Có chứa dấu phân cách ngày (/ hoặc -) VÀ KHÔNG chứa dấu giờ (:)
	hasSep := strings.Contains(s, "/") || strings.Contains(s, "-")
	hasTime := strings.Contains(s, ":")
	
	if hasSep && !hasTime {
		s += " 23:59:59"
	}

	// 4. Kiểm tra các định dạng quen thuộc VN
	layouts := []string{
		"02/01/2006 15:04:05", // dd/MM/yyyy HH:mm:ss
		"02-01-2006 15:04:05", // dd-MM-yyyy HH:mm:ss
		"2006-01-02 15:04:05", // yyyy-MM-dd HH:mm:ss
	}

	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, vnZone); err == nil {
			return t
		}
	}

	// 5. Thất bại -> Trả về time.Zero (Coi như hết hạn)
	return time.Time{}
}

// =================================================================================================
// ⚙️ PHẦN 3: HÀM HỖ TRỢ (HELPERS)
// =================================================================================================

// Kiểm tra Global Rate Limit (Có Reset mỗi giây)
func CheckGlobalRateLimit() bool {
	STATE.GlobalCounter.Mutex.Lock()
	defer STATE.GlobalCounter.Mutex.Unlock()

	now := time.Now().UnixMilli()
	// Nếu đã qua cửa sổ thời gian cũ -> Reset về 0
	if now-STATE.GlobalCounter.LastReset > TOKEN_RULES.WINDOW_MS {
		STATE.GlobalCounter.LastReset = now
		STATE.GlobalCounter.Count = 0
	}
	STATE.GlobalCounter.Count++
	return STATE.GlobalCounter.Count <= TOKEN_RULES.GLOBAL_MAX_REQ
}

// Kiểm tra User Rate Limit (Có Reset mỗi giây)
func CheckUserRateLimit(token string) bool {
	STATE.RateMutex.Lock()
	defer STATE.RateMutex.Unlock()

	now := time.Now().UnixMilli()
	rec, exists := STATE.RateLimit[token]
	// Nếu chưa có user này -> Tạo mới
	if !exists {
		rec = &RateLimitData{LastReset: now, Count: 0}
		STATE.RateLimit[token] = rec
	}

	// Reset nếu qua giây
	if now-rec.LastReset > TOKEN_RULES.WINDOW_MS {
		rec.LastReset = now
		rec.Count = 0
	}
	rec.Count++
	return rec.Count <= TOKEN_RULES.TOKEN_MAX_REQ
}

// Ghi dữ liệu vào Cache (Dùng chung cho cả Valid và Invalid token)
func setCache(token string, data *TokenData, isInvalid bool, msg string, ttl int64) {
	STATE.TokenMutex.Lock()
	defer STATE.TokenMutex.Unlock()
	
	cached := &CachedToken{
		IsInvalid:  isInvalid,
		Msg:        msg,
		ExpiryTime: time.Now().UnixMilli() + ttl,
	}
	if data != nil {
		cached.Data = *data
	}
	STATE.TokenCache[token] = cached
}

// Xóa Token khỏi Cache
func deleteTokenCache(token string) {
	STATE.TokenMutex.Lock()
	defer STATE.TokenMutex.Unlock()
	delete(STATE.TokenCache, token)
}
