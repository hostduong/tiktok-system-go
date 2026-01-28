package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// --- Handler Tạo Sheet (Stub hoạt động) ---
// Giữ nguyên để tool không lỗi, sau này có thể thêm logic tạo sheet thật bằng Google API
func HandleCreateSheets(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Sheets setup completed (Stub)"})
}

// --- Handler Xóa Cache ---
func HandleClearCache(w http.ResponseWriter, r *http.Request) {
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		http.Error(w, `{"status":"false","messenger":"Lỗi xác thực"}`, 401)
		return
	}
	sid := tokenData.SpreadsheetID

	// 1. Ép ghi toàn bộ dữ liệu đang chờ trong Queue xuống Google Sheet
	FlushQueue(sid, true)
	
	// 2. Xóa Cache RAM liên quan đến SpreadsheetID này
	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()
	
	prefix := sid + KEY_SEPARATOR
	count := 0
	for k := range STATE.SheetCache {
		if strings.HasPrefix(k, prefix) {
			delete(STATE.SheetCache, k)
			count++
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "true", 
		"messenger": "Đã xóa cache và đồng bộ dữ liệu",
		"deleted_keys": count,
	})
}
