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

type LogDataRequest struct {
	Type  string                   `json:"type"`
	Token string                   `json:"token"`
	Data  []map[string]interface{} `json:"data"`
}

func HandleLogData(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	var body LogDataRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return
	}

	q := queue.GetQueue(spreadsheetId, sheetSvc)

	for _, item := range body.Data {
		sheetName := "PostLogger"
		if s, ok := item["sheet"]; ok {
			sheetName = fmt.Sprintf("%v", s)
		}

		maxCol := 0
		for k := range item {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
				if idx > maxCol {
					maxCol = idx
				}
			}
		}

		row := make([]interface{}, maxCol+1)
		for i := range row {
			row[i] = ""
		}

		for k, v := range item {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
				if idx >= 0 && idx < len(row) {
					row[idx] = v
				}
			}
		}

		q.EnqueueAppend(sheetName, row)
	}

	utils.JSONResponse(w, "true", "Đang xử lý ghi dữ liệu", nil)
}

func HandleUpdatedCache(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service) {
	queue.GlobalQueues.Range(func(key, value interface{}) bool {
		q := value.(*queue.QueueManager)
		go q.Flush(false)
		return true
	})

	cache.GlobalSheets.Range(func(key, value interface{}) bool {
		cache.GlobalSheets.Delete(key)
		return true
	})

	GlobalMailCache.Range(func(key, value interface{}) bool {
		GlobalMailCache.Delete(key)
		return true
	})

	utils.JSONResponse(w, "true", "Cache cleared & Data flushed.", nil)
}

func HandleCreateSheets(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, authData map[string]interface{}) {
	utils.JSONResponse(w, "true", "Sheets dữ liệu đã được tạo", nil)
}
