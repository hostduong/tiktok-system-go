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
}

func HandleLogin(w http.ResponseWriter, r *http.Request, authSvc *auth.Authenticator, sheetSvc *sheets.Service) {
	var body LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.JSONResponse(w, "false", "JSON Error", nil)
		return
	}

	// 1. Verify Token
	valid, tokenData, msg := authSvc.VerifyToken(body.Token)
	if !valid {
		utils.JSONResponse(w, "false", msg, nil)
		return
	}

	// Lấy SpreadsheetID (từ token hoặc hardcode để test)
	spreadsheetID := tokenData.SpreadsheetID
	if spreadsheetID == "" {
		// Fallback ID nếu token không có (Bạn thay ID sheet thật vào đây nếu cần test nhanh)
		spreadsheetID = "1-DUMMY-ID-HAY-LAY-TU-ENV" 
	}

	sheetName := "DataTiktok"

	// 2. Xử lý Cache
	var cacheItem *cache.SheetCacheItem
	val, ok := cache.GlobalSheets.Load(spreadsheetID + "__" + sheetName)
	if ok {
		cacheItem = val.(*cache.SheetCacheItem)
	}

	// Nếu chưa có cache hoặc cache hết hạn -> Load lại từ Google Sheet
	if cacheItem == nil || !cacheItem.IsValid() {
		cacheItem = cache.NewSheetCache(spreadsheetID, sheetName)
		
		// Tải dữ liệu từ dòng 11 đến 10000
		accounts, err := sheetSvc.FetchData(spreadsheetID, sheetName, 11, 10000)
		if err != nil {
			utils.JSONResponse(w, "false", fmt.Sprintf("Lỗi tải Sheet: %v", err), nil)
			return
		}
		
		cacheItem.Lock()
		cacheItem.RawValues = accounts
		cacheItem.Unlock()
		
		// Xây dựng Index mới
		cacheItem.BuildIndex()
		
		cache.GlobalSheets.Store(spreadsheetID+"__"+sheetName, cacheItem)
	}

	// 3. Logic Tìm Kiếm Account
	// Ưu tiên tìm theo Email trước (vì Email là duy nhất trong Token)
	targetIndex := -1
	emailKey := strings.ToLower(tokenData.Email)

	cacheItem.RLock()
	if idx, found := cacheItem.IndexEmail[emailKey]; found {
		targetIndex = idx
	} else if idx, found := cacheItem.IndexUserID[tokenData.UID]; found {
		// Fallback tìm theo UserID
		targetIndex = idx
	}
	cacheItem.RUnlock()

	// 4. Trả kết quả
	if targetIndex != -1 {
		acc := cacheItem.GetAccountByIndex(targetIndex)
		if acc != nil {
			// Tự động gán DeviceId nếu chưa có
			if acc.DeviceId == "" {
				acc.DeviceId = body.DeviceId
				// Cần lưu lại sự thay đổi này (vào hàng đợi update sau này)
			}

			// Tách profile để trả về
			authProfile, activityProfile, aiProfile := SplitProfile(acc)
			
			resp := map[string]interface{}{
				"status":           "true",
				"messenger":        "Đăng nhập thành công",
				"row_index":        acc.RowIndex,
				"auth_profile":     authProfile,
				"activity_profile": activityProfile,
				"ai_profile":       aiProfile,
			}
			utils.JSONResponseRaw(w, resp)
			return
		}
	}

	// Trường hợp không tìm thấy (User mới)
	utils.JSONResponse(w, "false", "Tài khoản chưa được đăng ký trong hệ thống", nil)
}
