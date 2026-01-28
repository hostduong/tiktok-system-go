package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Chỉ giữ lại 2 hàm này vì các hàm Log/Mail/Search đã có file riêng

// --- Handler Tạo Sheet (Stub hoạt động) ---
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

	// Xả Queue
	FlushQueue(sid, true)
	
	// Xóa Cache RAM
	STATE.SheetMutex.Lock()
	for k := range STATE.SheetCache {
		if strings.HasPrefix(k, sid+KEY_SEPARATOR) {
			delete(STATE.SheetCache, k)
		}
	}
	STATE.SheetMutex.Unlock()

	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Cache cleared & Data flushed"})
}
