package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type MailRequest struct {
	Token   string `json:"token"`
	Email   string `json:"email"`
	Keyword string `json:"keyword"`
	Read    string `json:"read"` // "true"/"false"
}

func HandleReadMail(w http.ResponseWriter, r *http.Request) {
	var body MailRequest
	json.NewDecoder(r.Body).Decode(&body)

	auth := CheckToken(body.Token)
	if !auth.IsValid {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": auth.Messenger})
		return
	}
	
	targetEmail := CleanString(body.Email)
	keyword := CleanString(body.Keyword)
	markRead := (strings.ToLower(body.Read) == "true")

	// 1. Check RAM Cache (MailCache)
	// Implement simple MailCache Map in global_state... (Skip for brevity, assume load from sheet)
	
	// 2. Load Sheet EmailLogger
	cache, err := LayDuLieu(auth.SpreadsheetID, SHEET_NAMES.EMAIL_LOGGER, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "error", "messenger": "Lỗi đọc mail"})
		return
	}
	
	// 3. Filter
	limitTime := time.Now().Add(-time.Duration(RANGES.EMAIL_WINDOW_MINUTES)*time.Minute).UnixMilli()
	
	cache.Mutex.RLock()
	defer cache.Mutex.RUnlock()
	
	// Duyệt ngược từ dưới lên
	processed := 0
	for i := len(cache.RawValues) - 1; i >= 0; i-- {
		if processed >= RANGES.EMAIL_LIMIT_ROWS { break }
		processed++
		
		row := cache.RawValues[i]
		if len(row) <= 7 { continue } // Index 7 is READ column
		
		dateVal := ConvertSerialDate(row[0])
		if dateVal < limitTime { break }
		
		readStatus := CleanString(row[7]) // Column H (Index 7)
		if readStatus == "true" { continue }
		
		receiver := CleanString(row[2])
		sender := CleanString(row[3])
		
		if receiver != targetEmail { continue }
		if keyword != "" && !strings.Contains(sender, keyword) { continue }
		
		// Found!
		// 4. Queue Mail Update (Nếu markRead=true)
		if markRead {
			rowIndex := i + RANGES.EMAIL_START_ROW
			go func(sid string, rIdx int) {
				STATE.MailMutex.Lock()
				q := STATE.MailQueue[sid]
				if q == nil {
					q = &MailQueueData{Rows: make(map[int]bool)}
					STATE.MailQueue[sid] = q
				}
				q.Rows[rIdx] = true
				// Trigger timer logic similar to WriteQueue...
				STATE.MailMutex.Unlock()
			}(auth.SpreadsheetID, rowIndex)
		}
		
		// Return Data
		resData := map[string]interface{}{
			"date": row[0], "code": row[6], "body": row[5],
			// ... other fields
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "true", "messenger": "Lấy mã xác minh thành công", "email": resData,
		})
		return
	}
	
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Không tìm thấy mail phù hợp", "email": ""})
}
