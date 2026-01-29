package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

// =================================================================================================
// 📦 BIẾN TOÀN CỤC & CẤU TRÚC DỮ LIỆU
// =================================================================================================

// firebaseDB: Lưu kết nối database để dùng chung cho toàn bộ ứng dụng (tránh kết nối lại nhiều lần)
var firebaseDB *db.Client

// AuthInitError: Lưu lỗi nếu quá trình khởi tạo Firebase thất bại (để Middleware biết đường chặn)
var AuthInitError error

// ⚠️ LƯU Ý QUAN TRỌNG:
// Biến TOKEN_RULES đã được khai báo bên file config.go.
// Code ở file này sẽ tự động hiểu và lấy giá trị từ đó.
// Không khai báo lại ở đây để tránh lỗi "Redeclared in this block".

// TokenRequest: Struct dùng để hứng JSON từ client gửi lên.
// Dùng struct nhanh hơn map[string]interface{} về hiệu năng.
type TokenRequest struct {
	Token string `json:"token"` // Trường "token" trong JSON body
}

// =================================================================================================
// 🚀 PHẦN 1: KHỞI TẠO & MIDDLEWARE (CỔNG VÀO)
// =================================================================================================

// InitAuthService: Hàm này chạy 1 lần duy nhất khi Server khởi động (trong main.go).
// Nhiệm vụ: Kết nối tới Firebase Realtime Database.
func InitAuthService(credJSON []byte) {
	// Bước 1: Kiểm tra xem biến môi trường chứa Key có dữ liệu không
	if len(credJSON) == 0 {
		AuthInitError = fmt.Errorf("Dữ liệu Credential bị trống (Chưa set Env Var)")
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	// Bước 2: Chuẩn bị cấu hình kết nối
	ctx := context.Background()
	opt := option.WithCredentialsJSON(credJSON) // Dùng JSON Key để xác thực
	conf := &firebase.Config{
		// URL Database của Project Firebase (Phải chính xác 100%)
		DatabaseURL: "https://hostduong-1991-default-rtdb.asia-southeast1.firebasedatabase.app",
	}

	// Bước 3: Khởi tạo Firebase App
	app, err := firebase.NewApp(ctx, conf, opt)
	if err != nil {
		AuthInitError = fmt.Errorf("Lỗi khởi tạo Firebase App: %v", err)
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	// Bước 4: Lấy Client Database từ App
	client, err := app.Database(ctx)
	if err != nil {
		AuthInitError = fmt.Errorf("Lỗi kết nối Database: %v", err)
		log.Println("❌ [AUTH INIT] " + AuthInitError.Error())
		return
	}

	// Thành công: Gán vào biến toàn cục
	firebaseDB = client
	fmt.Println("✅ Firebase Service initialized (V4) - Documented Version.")
}

// AuthMiddleware: Đây là "Người bảo vệ" đứng trước mọi API.
// Nhiệm vụ: Chặn request rác, kiểm tra Token, giới hạn tốc độ.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		
		// 🛡️ LỚP 0: Kiểm tra quá tải Server (Global Rate Limit)
		// Nếu Server đang nhận quá 1000 req/s -> Từ chối ngay để bảo vệ CPU.
		if !CheckGlobalRateLimit() {
			http.Error(w, `{"status":"false","messenger":"Server Busy (Global Limit)"}`, 503)
			return
		}

		// Kiểm tra xem Database có đang kết nối ổn không
		if firebaseDB == nil {
			http.Error(w, `{"status":"false","messenger":"Database Connecting..."}`, 503)
			return
		}

		// 🛡️ ĐỌC DỮ LIỆU: Đọc Body JSON một cách an toàn
		// Cần đọc ra bytes rồi ghi lại vào Body để các hàm sau (như Login) có thể đọc lại.
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"status":"false","messenger":"Read Body Error"}`, 400)
			return
		}
		// "Tái sinh" body sau khi đã đọc
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Parse JSON để lấy Token ra kiểm tra
		var req TokenRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			// Nếu JSON sai định dạng -> Báo lỗi
			http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
			return
		}

		// Chuẩn hóa Token: Xóa khoảng trắng thừa đầu đuôi
		tokenStr := strings.TrimSpace(req.Token)
		
		// 🛡️ LỚP 1: Kiểm tra tính hợp lệ của Token (Core Logic)
		// Hàm này sẽ tự động check Cache RAM trước, nếu không có mới gọi Firebase.
		authRes := CheckToken(tokenStr)
		
		// Nếu Token KHÔNG hợp lệ
		if !authRes.IsValid {
			w.Header().Set("Content-Type", "application/json")
			
			status := "false" // Mặc định là lỗi có thể thử lại (false)
			
			// Phân loại lỗi: Nếu lỗi nghiêm trọng (Fatal) -> Trả về "error" để Client dừng luôn
			if isFatalError(authRes.Messenger) {
				status = "error"
			}

			// Trả về kết quả cho Client
			json.NewEncoder(w).Encode(map[string]string{
				"status":    status,
				"messenger": authRes.Messenger,
			})
			return // Dừng xử lý tại đây
		}

		// 🛡️ LỚP 2: Kiểm tra User Rate Limit (Công bằng)
		// Token đúng nhưng spam quá nhanh ( > 5 req/s) -> Chặn tạm thời.
		if !CheckUserRateLimit(tokenStr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429) // 429 = Too Many Requests
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Spam detected (Rate Limit)"})
			return
		}

		// ✅ THÀNH CÔNG: Lưu thông tin Token vào Context
		// Để các hàm xử lý phía sau (HandlerLogin, HandlerUpdate) có thể dùng ngay mà không cần query lại.
		ctx := context.WithValue(r.Context(), "tokenData", &TokenData{
			Token:         tokenStr,
			SpreadsheetID: authRes.SpreadsheetID,
			Data:          authRes.Data,
		})

		// Chuyển tiếp request đến hàm xử lý nghiệp vụ
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// =================================================================================================
// 🧠 PHẦN 2: LOGIC CHECK TOKEN & TIME PARSER (BỘ NÃO)
// =================================================================================================

// CheckToken: Hàm kiểm tra Token toàn diện (RAM -> DB -> Time Check)
func CheckToken(token string) AuthResult {
	// 1. Kiểm tra sơ bộ: Rỗng hoặc quá ngắn -> Loại ngay
	// TOKEN_RULES lấy từ config.go
	if token == "" || len(token) < TOKEN_RULES.MIN_LENGTH {
		return AuthResult{IsValid: false, Messenger: "Token không hợp lệ"} // Lỗi chết
	}

	now := time.Now().UnixMilli()

	// 2. KIỂM TRA CACHE RAM (Tốc độ cao)
	STATE.TokenMutex.RLock() // Khóa đọc (cho phép nhiều người đọc cùng lúc)
	cached, exists := STATE.TokenCache[token]
	STATE.TokenMutex.RUnlock() // Mở khóa ngay lập tức

	if exists {
		// Nếu tìm thấy trong Cache:
		if cached.IsInvalid {
			// Đây là Token rác đã bị nhớ (Negative Cache)
			if now < cached.ExpiryTime {
				// Vẫn trong thời gian phạt -> Chặn ngay
				return AuthResult{IsValid: false, Messenger: cached.Msg}
			}
			// Hết thời gian phạt -> Xóa cache để kiểm tra lại (biết đâu user đã gia hạn)
			deleteTokenCache(token)
		} else {
			// Đây là Token đúng đã được lưu (Positive Cache)
			if now < cached.ExpiryTime {
				// Vẫn còn hạn Cache -> Trả về thông tin ngay
				return AuthResult{IsValid: true, SpreadsheetID: cached.Data.SpreadsheetID, Data: cached.Data.Data}
			}
			// Hết hạn Cache -> Xóa để query lại Firebase lấy dữ liệu mới nhất
			deleteTokenCache(token)
		}
	}

	// 3. KIỂM TRA FIREBASE (Nếu Cache không có)
	if firebaseDB == nil {
		return AuthResult{IsValid: false, Messenger: "Database chưa sẵn sàng"}
	}

	// Tạo tham chiếu đến node chứa Token
	ref := firebaseDB.NewRef("TOKEN_TIKTOK/" + token)
	var data map[string]interface{}
	
	// Gọi API Firebase (Tốn thời gian mạng)
	if err := ref.Get(context.Background(), &data); err != nil {
		log.Printf("❌ [FIREBASE ERROR] %v", err)
		return AuthResult{IsValid: false, Messenger: "Lỗi kết nối Database"} // Cho phép thử lại
	}

	// --- PHÂN TÍCH KẾT QUẢ TỪ FIREBASE ---

	// Trường hợp: Token không tồn tại
	if data == nil {
		// Lưu vào Cache Chặn (để lần sau không phải hỏi Firebase nữa)
		setCache(token, nil, true, "Token không tồn tại", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token không tồn tại"}
	}

	// Trường hợp: Dữ liệu bị thiếu (Hư hỏng)
	if data["expired"] == nil {
		setCache(token, nil, true, "Token lỗi data (Thiếu expired)", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token lỗi data"}
	}
	if data["spreadsheetId"] == nil {
		setCache(token, nil, true, "Không có spreadsheetsId", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Không có spreadsheetsId"}
	}

	// 4. KIỂM TRA HẠN SỬ DỤNG (Smart Time Check)
	expStr := fmt.Sprintf("%v", data["expired"])
	expTime := parseSmartTime(expStr) // Dùng bộ parse thông minh
	
	timeLeft := expTime.Sub(time.Now()).Milliseconds()

	// Nếu parse lỗi hoặc thời gian còn lại <= 0 -> Hết hạn
	if expTime.IsZero() || timeLeft <= 0 {
		setCache(token, nil, true, "Token hết hạn", TOKEN_RULES.BLOCK_TTL_MS)
		return AuthResult{IsValid: false, Messenger: "Token hết hạn"}
	}

	// 5. CACHE THÀNH CÔNG (Token Ngon)
	sid := fmt.Sprintf("%v", data["spreadsheetId"])
	
	// Tính thời gian sống của Cache (TTL)
	// Cache sống = Min(Thời gian quy định, Thời gian còn lại của Token)
	// Để tránh việc Token hết hạn 10s nữa nhưng Cache vẫn lưu 60 phút.
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
	// Lưu vào Cache RAM
	setCache(token, &validData, false, "", ttl)

	// Trả về kết quả Thành công
	return AuthResult{IsValid: true, SpreadsheetID: sid, Data: data}
}

// parseSmartTime: Bộ phân tích thời gian đa năng
// Hỗ trợ: Timestamp số, ISO 8601, Ngày/Tháng/Năm VN...
func parseSmartTime(dateStr string) time.Time {
	// Ép cứng múi giờ Việt Nam (+7)
	vnZone := time.FixedZone("UTC+7", 7*3600)
	s := strings.TrimSpace(dateStr)

	// 1. Kiểm tra dạng số (Timestamp) - Ưu tiên cao nhất
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		// Nếu số lớn hơn 100 tỷ -> Là mili giây (vì 100 tỷ giây = năm 5138)
		if ts > 100_000_000_000 {
			return time.UnixMilli(ts).In(vnZone)
		}
		// Ngược lại là giây
		return time.Unix(ts, 0).In(vnZone)
	}

	// 2. Kiểm tra chuẩn Quốc tế (RFC3339 / ISO 8601)
	// Ví dụ: 2026-01-29T06:03:55+07:00
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(vnZone)
	}
	// Fallback: ISO thiếu Timezone -> Tự gán VN Zone
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, vnZone); err == nil {
		return t
	}

	// 3. Xử lý Date-Only (Chỉ có ngày, thiếu giờ)
	// Nếu có dấu phân cách ngày (/ hoặc -) và KHÔNG có dấu giờ (:)
	// -> Tự động cộng thêm 23:59:59 để tính hết hạn vào cuối ngày.
	if isDateOnly(s) {
		s += " 23:59:59"
	}

	// 4. Kiểm tra các định dạng Việt Nam phổ biến
	layouts := []string{
		"02/01/2006 15:04:05", // dd/MM/yyyy HH:mm:ss
		"02-01-2006 15:04:05", // dd-MM-yyyy HH:mm:ss
		"2006-01-02 15:04:05", // yyyy-MM-dd HH:mm:ss
	}

	for _, layout := range layouts {
		// ParseInLocation bắt buộc hiểu theo giờ VN
		if t, err := time.ParseInLocation(layout, s, vnZone); err == nil {
			return t
		}
	}

	// 5. Thất bại toàn tập -> Trả về time.Zero (Coi như hết hạn)
	return time.Time{}
}

// =================================================================================================
// 🛠️ PHẦN 3: CÁC HÀM HỖ TRỢ (HELPERS)
// =================================================================================================

// isFatalError: Xác định xem lỗi này có nghiêm trọng không.
// True = Lỗi chết (Dừng tool, status: error)
// False = Lỗi tạm thời (Thử lại sau, status: false)
func isFatalError(msg string) bool {
	// Chuẩn hóa chuỗi về chữ thường và xóa khoảng trắng thừa
	msg = strings.ToLower(strings.TrimSpace(msg))

	// Lọc nhanh: Nếu không bắt đầu bằng các từ khóa chính -> Không phải lỗi Auth
	// (Đây là logic phòng thủ để tránh bắt nhầm lỗi khác)
	if !strings.HasPrefix(msg, "token") && !strings.HasPrefix(msg, "không có") {
		return false
	}

	// Kiểm tra từ khóa
	switch {
	case strings.Contains(msg, "không tồn tại"), // Token sai
		strings.Contains(msg, "hết hạn"),       // Token cũ
		strings.Contains(msg, "không hợp lệ"),  // Format sai
		strings.Contains(msg, "bị block"),      // Bị admin chặn
		strings.Contains(msg, "lỗi data"),      // Thiếu trường expired
		strings.Contains(msg, "spreadsheetsid"): // Thiếu ID Sheet
		return true // Đây là lỗi CHẾT
	}
	return false // Các lỗi khác (mạng, db...) cho phép thử lại
}

// isDateOnly: Kiểm tra xem chuỗi có phải chỉ chứa ngày không
func isDateOnly(s string) bool {
	hasSep := strings.Contains(s, "/") || strings.Contains(s, "-")
	hasTime := strings.Contains(s, ":")
	return hasSep && !hasTime // Có gạch ngày nhưng không có hai chấm giờ
}

// CheckGlobalRateLimit: Kiểm tra giới hạn tổng Server (Lớp 0)
func CheckGlobalRateLimit() bool {
	STATE.GlobalCounter.Mutex.Lock()
	defer STATE.GlobalCounter.Mutex.Unlock()

	now := time.Now().UnixMilli()
	// TOKEN_RULES lấy từ config.go (Không cần khai báo lại ở đây)
	if now-STATE.GlobalCounter.LastReset > TOKEN_RULES.WINDOW_MS {
		STATE.GlobalCounter.LastReset = now
		STATE.GlobalCounter.Count = 0
	}
	STATE.GlobalCounter.Count++
	// Trả về True nếu chưa vượt quá giới hạn
	return STATE.GlobalCounter.Count <= TOKEN_RULES.GLOBAL_MAX_REQ
}

// CheckUserRateLimit: Kiểm tra giới hạn từng User (Lớp 2)
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

	// Reset nếu qua cửa sổ thời gian
	if now-rec.LastReset > TOKEN_RULES.WINDOW_MS {
		rec.LastReset = now
		rec.Count = 0
	}
	rec.Count++
	// Trả về True nếu chưa spam
	return rec.Count <= TOKEN_RULES.TOKEN_MAX_REQ
}

// setCache: Hàm ghi dữ liệu vào Cache RAM an toàn (Thread-safe)
func setCache(token string, data *TokenData, isInvalid bool, msg string, ttl int64) {
	STATE.TokenMutex.Lock()
	defer STATE.TokenMutex.Unlock() // Đảm bảo luôn mở khóa khi xong việc
	
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

// deleteTokenCache: Hàm xóa Cache an toàn
func deleteTokenCache(token string) {
	STATE.TokenMutex.Lock()
	defer STATE.TokenMutex.Unlock() // Đảm bảo luôn mở khóa
	delete(STATE.TokenCache, token)
}
