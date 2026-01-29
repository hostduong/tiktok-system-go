package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HandleMailData: Xử lý ghi log mail (Dùng chung cơ chế QueueAppend)
func HandleMailData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi Body JSON"}`, 400)
		return
	}

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		http.Error(w, `{"status":"false","messenger":"Lỗi xác thực"}`, 401)
		return
	}

	dataList, _ := body["data"].([]interface{})
	if len(dataList) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Không có dữ liệu mail"})
		return
	}

	// Gom nhóm theo Sheet Name (thường là EmailLogger)
	rowsBySheet := make(map[string][][]interface{})
	defaultSheet := SHEET_NAMES.EMAIL_LOGGER

	for _, item := range dataList {
		obj, ok := item.(map[string]interface{})
		if !ok { continue }

		targetSheet := defaultSheet
		if s, ok := obj["sheet"].(string); ok && s != "" {
			targetSheet = s
		}

		// Tạo row
		maxCol := 0
		for k := range obj {
			if strings.HasPrefix(k, "col_") {
				if idx, err := strconv.Atoi(k[4:]); err == nil && idx > maxCol {
					maxCol = idx
				}
			}
		}

		row := make([]interface{}, maxCol+1)
		for i := range row { row[i] = "" }

		for k, v := range obj {
			if strings.HasPrefix(k, "col_") {
				if idx, err := strconv.Atoi(k[4:]); err == nil {
					row[idx] = v
				}
			}
		}
		
		// Thêm timestamp nếu cần (tùy logic cũ, ở đây giữ nguyên input client)
		rowsBySheet[targetSheet] = append(rowsBySheet[targetSheet], row)
	}

	// Đẩy xuống Queue chung (Không dùng MailQueue riêng nữa)
	for sheet, rows := range rowsBySheet {
		if len(rows) > 0 {
			QueueAppend(tokenData.SpreadsheetID, sheet, rows)
		}
	}

	// Clean cache nếu cần (mail thường không cần clear cache ngay)
	
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Đã tiếp nhận mail log"})
}

// HandleGetMail: Lấy mail từ cache (Nếu cần logic đọc mail)
// Nếu bạn chưa dùng hàm này thì có thể comment lại, nhưng tôi viết mẫu theo chuẩn mới
func HandleGetMail(w http.ResponseWriter, r *http.Request) {
	// ... Logic đọc mail từ Sheet ...
	// Tạm thời trả về Stub để không lỗi build
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Mail feature ready"})
}

// Xóa mail cũ (Cleanup)
func CleanupOldMails() {
	for {
		time.Sleep(10 * time.Minute)
		// Logic xóa mail cũ... (Implement sau)
	}
}
