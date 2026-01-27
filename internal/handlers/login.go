package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"tiktok-server/internal/auth"
	"tiktok-server/internal/cache"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

type LoginRequest struct {
	Type     string `json:"type"`
	Token    string `json:"token"`
	DeviceId string `json:"deviceId"`
	Action   string `json:"action"` // login, register, auto...
}

func HandleLogin(w http.ResponseWriter, r *http.Request, authSvc *auth.Authenticator, sheetSvc *sheets.Service) {
	var body LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.JSONResponse(w, "false", "Lỗi định dạng JSON", nil)
		return
	}

	// 1. VERIFY TOKEN (Giống Node.js: xu_ly_dong_bo_firebase)
	valid, tokenData, msg := authSvc.VerifyToken(body.Token)
	if !valid {
		utils.JSONResponse(w, "false", msg, nil)
		return
	}

	spreadsheetID := tokenData.SpreadsheetID
	deviceID := strings.TrimSpace(body.DeviceId)
	sheetName := "DataTiktok"

	if deviceID == "" {
		utils.JSONResponse(w, "false", "Thiếu deviceId", nil)
		return
	}

	// 2. LOAD DATA & CACHE (Giống Node.js: GoogleService.lay_du_lieu)
	// Key cache: SID__DataTiktok
	cacheKey := spreadsheetID + "__" + sheetName
	var cacheItem *cache.SheetCacheItem
	
	val, ok := cache.GlobalSheets.Load(cacheKey)
	if ok {
		cacheItem = val.(*cache.SheetCacheItem)
	}

	// Nếu chưa có cache hoặc cache cũ -> Load lại từ Google
	if cacheItem == nil || !cacheItem.IsValid() {
		cacheItem = cache.NewSheetCache(spreadsheetID, sheetName)
		
		// Tải dữ liệu từ dòng 11 đến 10000 (Giống CONFIG.RANGES.DATA_MAX_ROW)
		accounts, err := sheetSvc.FetchData(spreadsheetID, sheetName, 11, 10000)
		if err != nil {
			utils.JSONResponse(w, "false", fmt.Sprintf("Lỗi tải Sheet: %v", err), nil)
			return
		}
		
		cacheItem.Lock()
		cacheItem.RawValues = accounts
		cacheItem.Unlock()
		
		cacheItem.BuildIndex() // Tạo IndexUserID, IndexEmail...
		cache.GlobalSheets.Store(cacheKey, cacheItem)
	}

	// 3. TÌM KIẾM NICK (Giống Node.js: xu_ly_tim_kiem)
	// Logic Optimistic Locking: 
	// - Ưu tiên 1: Nick cũ của Device này.
	// - Ưu tiên 2: Nick chưa có DeviceID.
	
	targetIndex := -1
	
	// Tìm trong danh sách nick
	// Node.js dùng hàm `xu_ly_tim_kiem` phức tạp với Priority Groups.
	// Ở đây ta mô phỏng logic cốt lõi nhất để chạy được:
	
	cacheItem.Lock() // Khóa để tránh tranh chấp (Locking)
	
	// A. Tìm nick cũ của thiết bị này (Re-login)
	for i, acc := range cacheItem.RawValues {
		if acc.DeviceId == deviceID {
			targetIndex = i
			break
		}
	}

	// B. Nếu không có nick cũ, tìm nick mới (Trống DeviceId)
	if targetIndex == -1 {
		// Duyệt qua index status "đang chờ" hoặc "hoàn thành" tùy action
		// Để đơn giản và nhanh, ta duyệt mảng (vì đã cache RAM)
		for i, acc := range cacheItem.RawValues {
			// Chỉ lấy nick chưa có DeviceID và có Email/Pass
			if acc.DeviceId == "" && acc.Email != "" && acc.Password != "" {
				// OPTIMISTIC LOCKING: Ghi đè ngay
				acc.DeviceId = deviceID
				acc.Status = "Đang chạy" // Update status luôn
				targetIndex = i
				
				// Đánh dấu cần update xuống Sheet (Queue)
				// TODO: Gọi Queue update (sẽ làm ở bước hoàn thiện)
				break
			}
		}
	}
	cacheItem.Unlock()

	// 4. TRẢ KẾT QUẢ (Response giống hệt Node.js)
	if targetIndex != -1 {
		acc := cacheItem.GetAccountByIndex(targetIndex)
		
		// Tách profile bằng hàm Common đã tạo
		authProfile, activityProfile, aiProfile := SplitProfile(acc)

		resp := map[string]interface{}{
			"status":           "true",
			"type":             "login", // Hoặc register tùy logic
			"messenger":        "Lấy nick thành công",
			"deviceId":         deviceID,
			"row_index":        acc.RowIndex,
			"system_email":     acc.Email, // Email hệ thống cần
			"auth_profile":     authProfile,
			"activity_profile": activityProfile,
			"ai_profile":       aiProfile,
		}
		utils.JSONResponseRaw(w, resp)
	} else {
		utils.JSONResponse(w, "false", "Không còn tài khoản phù hợp", nil)
	}
}
