package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func HandleMailData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	dataList, _ := body["data"].([]interface{})
	rowsBySheet := make(map[string][][]interface{})

	for _, item := range dataList {
		obj, ok := item.(map[string]interface{})
		if !ok { continue }
		sheet := SHEET_NAMES.EMAIL_LOGGER
		if s, ok := obj["sheet"].(string); ok && s != "" { sheet = s }

		maxCol := 0
		for k := range obj {
			if strings.HasPrefix(k, "col_") {
				if idx, err := strconv.Atoi(k[4:]); err == nil && idx > maxCol { maxCol = idx }
			}
		}
		row := make([]interface{}, maxCol+1)
		for i := range row { row[i] = "" }
		for k, v := range obj {
			if strings.HasPrefix(k, "col_") {
				if idx, err := strconv.Atoi(k[4:]); err == nil { row[idx] = v }
			}
		}
		rowsBySheet[sheet] = append(rowsBySheet[sheet], row)
	}

	for s, r := range rowsBySheet {
		if len(r) > 0 { QueueAppend(tokenData.SpreadsheetID, s, r) }
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Đã tiếp nhận mail log"})
}

// 🔥 SỬA TÊN HÀM Ở ĐÂY CHO KHỚP VỚI MAIN.GO
func HandleReadMail(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { http.Error(w, "Unauthorized", 401); return }

	sid := tokenData.SpreadsheetID
	email := CleanString(body["email"])
	keyword := CleanString(body["keyword"])
	markRead := fmt.Sprintf("%v", body["read"]) == "true"

	cacheData, err := LayDuLieu(sid, SHEET_NAMES.EMAIL_LOGGER, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Lỗi đọc dữ liệu"})
		return
	}

	STATE.SheetMutex.RLock()
	rows := cacheData.RawValues
	
	var resultData map[string]interface{}
	found := false
	targetIdx := -1
	
	limitTime := time.Now().Add(time.Duration(-RANGES.EMAIL_WINDOW_MINUTES) * time.Minute).UnixMilli()
	processCount := 0
	
	for i := len(rows) - 1; i >= 0; i-- {
		if processCount >= RANGES.EMAIL_LIMIT_ROWS { break }
		processCount++
		
		row := rows[i]
		if len(row) <= 7 { continue }

		mailTime := ConvertSerialDate(row[0])
		if mailTime < limitTime { break }

		if fmt.Sprintf("%v", row[6]) == "" { continue }
		if CleanString(row[7]) == "true" { continue }
		if CleanString(row[2]) != email { continue }
		if keyword != "" && !strings.Contains(CleanString(row[3]), keyword) { continue }

		found = true
		targetIdx = i
		resultData = map[string]interface{}{
			"date": row[0], "sender_name": row[1], "receiver_email": row[2],
			"sender_email": row[3], "subject": row[4], "body": row[5], "code": row[6],
		}
		break
	}
	STATE.SheetMutex.RUnlock()

	if found && markRead {
		STATE.SheetMutex.RLock()
		newRow := make([]interface{}, len(rows[targetIdx]))
		copy(newRow, rows[targetIdx])
		STATE.SheetMutex.RUnlock()
		
		newRow[7] = "TRUE"
		QueueUpdate(sid, SHEET_NAMES.EMAIL_LOGGER, targetIdx, newRow)
	}

	if found {
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "messenger": "Lấy mã thành công", "email": resultData})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "messenger": "Không tìm thấy mail", "email": map[string]interface{}{}})
	}
}

func CleanupOldMails() {}
