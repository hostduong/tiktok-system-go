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

const MailCacheTTL = 10 * time.Second

type MailCacheItem struct {
	Data      map[string]interface{}
	ExpiresAt time.Time
}

var GlobalMailCache = sync.Map{}

type ReadMailRequest struct {
	Type    string `json:"type"`
	Token   string `json:"token"`
	Email   string `json:"email"`
	Keyword string `json:"keyword"`
	Read    string `json:"read"`
}

func HandleReadMail(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	var body ReadMailRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return
	}

	email := utils.NormalizeString(body.Email)
	keyword := utils.NormalizeString(body.Keyword)
	markRead := (strings.ToLower(strings.TrimSpace(body.Read)) == "true")

	cacheKey := fmt.Sprintf("%s_%s_%s", spreadsheetId, email, keyword)
	if val, ok := GlobalMailCache.Load(cacheKey); ok {
		item := val.(MailCacheItem)
		if time.Now().Before(item.ExpiresAt) {
			utils.JSONResponseRaw(w, item.Data)
			return
		}
		GlobalMailCache.Delete(cacheKey)
	}

	limitTime := time.Now().Add(-60 * time.Minute)
	startRow := 112
	rawRows, err := sheetSvc.FetchRawData(spreadsheetId, "EmailLogger", startRow, startRow+500)
	if err != nil {
		utils.JSONResponse(w, "false", "Lỗi đọc mail", nil)
		return
	}

	var resultData map[string]interface{}

	if keyword != "" {
		for i := len(rawRows) - 1; i >= 0; i-- {
			row := rawRows[i]
			if len(row) < 8 {
				continue
			}

			dateStr := fmt.Sprintf("%v", row[0])
			mailTime := parseExcelTime(dateStr)
			if mailTime.Before(limitTime) {
				break
			}

			code := fmt.Sprintf("%v", row[6])
			if code == "" {
				continue
			}

			isRead := strings.ToLower(fmt.Sprintf("%v", row[7])) == "true"
			if isRead {
				continue
			}

			rowReceiver := utils.NormalizeString(fmt.Sprintf("%v", row[2]))
			rowSender := utils.NormalizeString(fmt.Sprintf("%v", row[3]))

			if rowReceiver == email && strings.Contains(rowSender, keyword) {
				if markRead {
					q := queue.GetQueue(spreadsheetId, sheetSvc)
					q.EnqueueMailUpdate(startRow + i)
				}

				emailObj := map[string]interface{}{
					"date":           row[0],
					"sender_name":    row[1],
					"receiver_email": row[2],
					"sender_email":   row[3],
					"subject":        row[4],
					"body":           row[5],
					"code":           row[6],
				}
				resultData = map[string]interface{}{
					"status":    "true",
					"messenger": "Lấy mã xác minh thành công",
					"email":     emailObj,
				}
				break
			}
		}
	} else {
		resultList := make(map[string]interface{})
		count := 0
		processed := 0
		for i := len(rawRows) - 1; i >= 0; i-- {
			if processed >= 500 {
				break
			}
			processed++

			row := rawRows[i]
			if len(row) < 8 {
				continue
			}

			dateStr := fmt.Sprintf("%v", row[0])
			if parseExcelTime(dateStr).Before(limitTime) {
				break
			}

			isRead := strings.ToLower(fmt.Sprintf("%v", row[7])) == "true"
			if isRead {
				continue
			}

			rowReceiver := utils.NormalizeString(fmt.Sprintf("%v", row[2]))
			if rowReceiver != email {
				continue
			}

			resultList[fmt.Sprintf("%d", count)] = map[string]interface{}{
				"date":           row[0],
				"sender_name":    row[1],
				"receiver_email": row[2],
				"sender_email":   row[3],
				"subject":        row[4],
				"body":           row[5],
				"code":           row[6],
			}
			count++
			if count >= 100 {
				break
			}
		}
		if count > 0 {
			resultData = map[string]interface{}{
				"status":    "true",
				"messenger": "Lấy danh sách email thành công",
				"email":     resultList,
			}
		}
	}

	if resultData == nil {
		resultData = map[string]interface{}{
			"status":    "true",
			"messenger": "Không tìm thấy mail phù hợp",
			"email":     map[string]interface{}{},
		}
	}

	GlobalMailCache.Store(cacheKey, MailCacheItem{
		Data:      resultData,
		ExpiresAt: time.Now().Add(MailCacheTTL),
	})

	utils.JSONResponseRaw(w, resultData)
}

func parseExcelTime(v string) time.Time {
	return time.Now()
}
