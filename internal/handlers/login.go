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
		utils.JSONResponse(w, "false", "Lỗi JSON input", nil)
		return
	}

	// 1. GỌI DB FIREBASE ĐỂ CHECK KEY (Code mới)
	valid, tokenData, msg := authSvc.VerifyToken(body.Token)
	if !valid {
		// Trả về đúng format lỗi để Tool nhận diện
		utils.JSONResponse(w, "false", msg, nil)
		return
	}

	// 2. LẤY SHEET ID TỪ KẾT QUẢ DB
	spreadsheetID := tokenData.SpreadsheetID
	deviceID := strings.TrimSpace(body.DeviceId)
	
	// ... (Đoạn dưới giữ nguyên logic Cache & Search như cũ) ...
	// Nếu bạn lười copy lại, thì đây là đoạn ngắn gọn để test:
	
	if deviceID == "" {
		utils.JSONResponse(w, "false", "Thiếu Device ID", nil)
		return
	}

	// Cache Logic (Tóm tắt)
	sheetName := "DataTiktok"
	cacheKey := spreadsheetID + "__" + sheetName
	var cacheItem *cache.SheetCacheItem
	if val, ok := cache.GlobalSheets.Load(cacheKey); ok {
		cacheItem = val.(*cache.SheetCacheItem)
	}

	if cacheItem == nil || !cacheItem.IsValid() {
		// Load mới
		cacheItem = cache.NewSheetCache(spreadsheetID, sheetName)
		accounts, err := sheetSvc.FetchData(spreadsheetID, sheetName, 11, 10000)
		if err != nil {
			utils.JSONResponse(w, "false", fmt.Sprintf("Lỗi đọc Sheet: %v", err), nil)
			return
		}
		cacheItem.Lock()
		cacheItem.RawValues = accounts
		cacheItem.Unlock()
		cacheItem.BuildIndex()
		cache.GlobalSheets.Store(cacheKey, cacheItem)
	}

	// Logic tìm nick đơn giản (Login)
	cacheItem.Lock()
	defer cacheItem.Unlock()
	
	targetIdx := -1
	// Ưu tiên 1: Nick cũ
	for i, acc := range cacheItem.RawValues {
		if acc.DeviceId == deviceID {
			targetIdx = i
			break
		}
	}
	// Ưu tiên 2: Nick mới
	if targetIdx == -1 {
		for i, acc := range cacheItem.RawValues {
			if acc.DeviceId == "" && acc.Status == "Đang chờ" { // Ví dụ trạng thái
				acc.DeviceId = deviceID
				targetIdx = i
				break
			}
		}
	}

	if targetIdx != -1 {
		acc := cacheItem.RawValues[targetIdx]
		// Gọi hàm SplitProfile (đã có ở file common.go)
		p1, p2, p3 := SplitProfile(acc)
		
		utils.JSONResponseRaw(w, map[string]interface{}{
			"status": "true",
			"messenger": "Lấy nick thành công",
			"row_index": acc.RowIndex,
			"auth_profile": p1,
			"activity_profile": p2,
			"ai_profile": p3,
		})
	} else {
		utils.JSONResponse(w, "false", "Hết nick phù hợp", nil)
	}
}
