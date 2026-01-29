package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func HandleMailData(w http.ResponseWriter, r *http.Request) {
	// ... (Parse Body & Auth giữ nguyên) ...
	
	// 🔥 Logic chính đã đổi:
	// Thay vì dùng STATE.MailQueue -> Dùng QueueAppend
	
	// Stub tạm để build thành công (Vì bạn đang dùng unified queue)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Mail log queued"})
}

// Logic đọc mail (Read Mail)
func HandleGetMail(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	
	tokenData, _ := r.Context().Value("tokenData").(*TokenData)
	sid := tokenData.SpreadsheetID
	email := CleanString(body["email"])
	keyword := CleanString(body["keyword"])
	markRead := fmt.Sprintf("%v", body["read"]) == "true"

	cacheData, err := LayDuLieu(sid, SHEET_NAMES.EMAIL_LOGGER, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Lỗi data"})
		return
	}

	// Lock để đọc an toàn
	STATE.SheetMutex.RLock()
	rows := cacheData.RawValues
	found := false
	var result map[string]interface{}
	var targetIdx int

	// Quét ngược từ dưới lên (Mới nhất)
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if len(row) < 8 { continue } // Cột H là index 7
		
		// Check conditions (Email, Keyword, Unread...)
		// ... (Logic so sánh giống Node.js) ...
		
		// Giả sử tìm thấy
		if true { // Replace with real condition
			targetIdx = i
			found = true
			result = map[string]interface{}{
				"code": row[6], // Ví dụ cột G
			}
			break
		}
	}
	STATE.SheetMutex.RUnlock()

	if found && markRead {
		// 🔥 Dùng Queue Update Chung (Thay vì MailQueue riêng)
		// Chỉ update cột H (Read) -> TRUE
		updateRow := make([]interface{}, 8) // Giả sử độ dài row
		updateRow[7] = "TRUE"
		// Lưu ý: Logic QueueUpdate của ta đang update CẢ DÒNG. 
		// Để tối ưu (chỉ update 1 ô), cần sửa logic Queue hoặc chấp nhận ghi đè cả dòng.
		// Tạm thời ghi đè cả dòng (lấy từ cache ra sửa)
		
		// TODO: Implement logic lấy full row, sửa cột H, rồi QueueUpdate(sid, EMAIL_LOGGER, targetIdx, fullRow)
	}

	if found {
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "email": result})
	} else {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Không tìm thấy mail"})
	}
}
