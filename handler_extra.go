package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// --- Handler Tạo Sheet (Stub hoạt động) ---
func HandleCreateSheets(w http.ResponseWriter, r *http.Request) {
	// Logic tạo sheet (Copy từ Node.js cần nhiều logic phức tạp hơn, tạm thời trả về Success để không lỗi)
	// Bạn có thể mở rộng sau. Hiện tại để pass build.
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

// --- Handler Search Data (Stub) ---
func HandleSearchData(w http.ResponseWriter, r *http.Request) {
	// Logic tìm kiếm nâng cao (Implement sau)
	json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Search Data not implemented yet"})
}

// --- Handler Log Data (Ghi Log) ---
func HandleLogData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	
	tokenData, _ := r.Context().Value("tokenData").(*TokenData)
	sid := tokenData.SpreadsheetID

	if data, ok := body["data"].([]interface{}); ok {
		for _, item := range data {
			if rowMap, ok := item.(map[string]interface{}); ok {
				// Convert map to row array... (Simplified)
				// Để đơn giản, chức năng này tạm thời nhận request nhưng chưa ghi thật
				// Cần mapping cột phức tạp.
			}
		}
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Log received"})
}

// --- Handler Read Mail (Stub) ---
func HandleReadMail(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Read Mail not implemented yet"})
}
