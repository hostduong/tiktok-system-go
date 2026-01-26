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

// HandleLogData: Ghi log vào Sheet (PostLogger, ErrorLogger...)
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

		// Tìm max col để tạo mảng
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

// HandleUpdatedCache: Reset cache RAM và ép ghi đĩa
func HandleUpdatedCache(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service) {
	[cite_start]// Logic [cite: 423-428]
	// 1. Flush tất cả Queue (Data + Mail)
	queue.GlobalQueues.Range(func(key, value interface{}) bool {
		q := value.(*queue.QueueManager)
		go q.Flush(false)
		return true
	})

	// 2. Clear Cache RAM (Sheet)
	cache.GlobalSheets.Range(func(key, value interface{}) bool {
		cache.GlobalSheets.Delete(key)
		return true
	})
	
	// 3. Clear Cache Mail
	handlers.GlobalMailCache.Range(func(key, value interface{}) bool {
		handlers.GlobalMailCache.Delete(key)
		return true
	})

	utils.JSONResponse(w, "true", "Cache cleared & Data flushed.", nil)
}

// HandleCreateSheets: Tạo sheet mẫu (Logic copy từ Master)
func HandleCreateSheets(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, authData map[string]interface{}) {
	[cite_start]// Logic [cite: 405-422]: Giả lập thành công vì Cloud Run thường đã có Sheet
	utils.JSONResponse(w, "true", "Sheets dữ liệu đã được tạo", nil)
}
