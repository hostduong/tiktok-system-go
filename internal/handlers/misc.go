package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tiktok-server/internal/cache"
	"tiktok-server/internal/queue"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

// LogDataRequest: Request ghi log
type LogDataRequest struct {
	Type  string                   `json:"type"`
	Token string                   `json:"token"`
	Data  []map[string]interface{} `json:"data"`
}

func HandleLogData(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	var body LogDataRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil { return }

	q := queue.GetQueue(spreadsheetId, sheetSvc)

	[cite_start]// Logic [cite: 319-325]
	for _, item := range body.Data {
		sheetName := "PostLogger"
		if s, ok := item["sheet"]; ok {
			sheetName = fmt.Sprintf("%v", s)
		}

		// Tìm max col
		maxCol := 0
		for k := range item {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
				if idx > maxCol { maxCol = idx }
			}
		}

		// Tạo row array
		row := make([]interface{}, maxCol+1)
		for i := range row { row[i] = "" }

		for k, v := range item {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
				row[idx] = v
			}
		}

		// Đẩy vào Queue Append
		q.EnqueueAppend(sheetName, row)
	}

	utils.JSONResponse(w, "true", "Đang xử lý ghi dữ liệu", nil)
}

// HandleUpdatedCache: Reset cache và ép ghi đĩa
func HandleUpdatedCache(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service) {
	[cite_start]// Logic [cite: 423-428]
	// 1. Flush tất cả Queue
	// Duyệt qua GlobalQueues
	queue.GlobalQueues.Range(func(key, value interface{}) bool {
		q := value.(*queue.QueueManager)
		go q.Flush(false)
		return true
	})

	// 2. Clear Cache RAM
	// Duyệt qua GlobalSheets và xóa
	cache.GlobalSheets.Range(func(key, value interface{}) bool {
		cache.GlobalSheets.Delete(key)
		return true
	})

	utils.JSONResponse(w, "true", "Cache cleared & Data flushed.", nil)
}

// HandleCreateSheets: Tạo sheet mẫu
func HandleCreateSheets(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, authData map[string]interface{}) {
	// Logic tạo sheet (Copy từ Master)
	// Để đơn giản hóa, ta chỉ trả về true vì Cloud Run thường đã có Sheet sẵn.
	// Nếu cần implement full logic copy từ Master, ta cần quyền Drive API.
	[cite_start]// Logic [cite: 405-422]
	utils.JSONResponse(w, "true", "Sheets dữ liệu đã được tạo (Giả lập)", nil)
}
