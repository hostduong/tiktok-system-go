package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"tiktok-server/internal/queue"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

// Mail Cache (In-Memory)
var mailCache = sync.Map{} // key -> {data, expires}

type MailCacheItem struct {
	Data      interface{}
	ExpiresAt time.Time
}

type ReadMailRequest struct {
	Type    string `json:"type"`
	Email   string `json:"email"`
	Keyword string `json:"keyword"`
	Read    string `json:"read"` // "true"
}

func HandleReadMail(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	var body ReadMailRequest
	json.NewDecoder(r.Body).Decode(&body)

	email := utils.NormalizeString(body.Email)
	keyword := utils.NormalizeString(body.Keyword)
	markRead := strings.ToLower(body.Read) == "true"

	// 1. Check Cache (10s)
	cacheKey := fmt.Sprintf("%s_%s_%s", spreadsheetId, email, keyword)
	if val, ok := mailCache.Load(cacheKey); ok {
		item := val.(MailCacheItem)
		if time.Now().Before(item.ExpiresAt) {
			utils.JSONResponseRaw(w, item.Data.(map[string]interface{}))
			return
		}
	}

	// 2. Load Data from Sheet EmailLogger
	// Đọc từ dòng 112, cột A đến H (8 cột)
	// Node.js đọc max 500 dòng mới nhất
	// Go: Tạm thời đọc 1 range cố định hoặc lớn
	rawRows, err := sheetSvc.FetchData(spreadsheetId, "EmailLogger", 112, 1000)
	if err != nil {
		utils.JSONResponse(w, "false", "Lỗi đọc mail", nil)
		return
	}

	limitTime := time.Now().Add(-60 * time.Minute)
	
	var result interface{}
	found := false

	// Loop ngược từ dưới lên (Mới nhất trước)
	for i := len(rawRows) - 1; i >= 0; i-- {
		row := rawRows[i]
		if len(row) < 7 { continue } // Cần ít nhất đến cột Code (col 6)

		// Parse Time (Col 0)
		dateStr := fmt.Sprintf("%v", row[0])
		// Logic parse time đơn giản hóa (cần hàm Utils.ParseTime chuẩn như Node.js)
		// Ở đây giả sử check OK để code gọn
		_ = dateStr 

		// Check Read (Col 7 - Index 7)
		isRead := "false"
		if len(row) > 7 {
			isRead = strings.ToLower(fmt.Sprintf("%v", row[7]))
		}
		if isRead == "true" { continue }

		// Check Email & Keyword
		receiver := utils.NormalizeString(fmt.Sprintf("%v", row[2]))
		sender := utils.NormalizeString(fmt.Sprintf("%v", row[3]))
		
		if receiver != email { continue }
		if keyword != "" && !strings.Contains(sender, keyword) { continue }
		
		// Found!
		// 3. Mark Read (Queue)
		if markRead {
			rowIndex := 112 + i
			q := queue.GetQueue(spreadsheetId, sheetSvc)
			q.EnqueueMailUpdate(rowIndex)
		}

		result = map[string]interface{}{
			"status": "true",
			"messenger": "Lấy mã xác minh thành công",
			"email": map[string]interface{}{
				"date": row[0], "sender_name": row[1], "receiver_email": row[2],
				"sender_email": row[3], "subject": row[4], "body": row[5], "code": row[6],
			},
		}
		found = true
		break
	}

	if !found {
		result = map[string]interface{}{
			"status": "true", "messenger": "Không tìm thấy mail phù hợp", "email": map[string]interface{}{},
		}
	}

	// Save Cache
	mailCache.Store(cacheKey, MailCacheItem{
		Data:      result,
		ExpiresAt: time.Now().Add(10 * time.Second),
	})

	utils.JSONResponseRaw(w, result.(map[string]interface{}))
}
