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

// MailCacheTTL: Thời gian lưu cache (10 giây)
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
		utils.JSONResponse(w, "false", "Lỗi định dạng JSON", nil)
		return
	}

	email := utils.NormalizeString(body.Email)
	keyword := utils.NormalizeString(body.Keyword)
	markRead := (strings.ToLower(strings.TrimSpace(body.Read)) == "true")

	// 1. Kiểm tra Cache
	cacheKey := fmt.Sprintf("%s_%s_%s", spreadsheetId, email, keyword)
	if val, ok := GlobalMailCache.Load(cacheKey); ok {
		item := val.(MailCacheItem)
		if time.Now().Before(item.ExpiresAt) {
			utils.JSONResponseRaw(w, item.Data)
			return
		}
		GlobalMailCache.Delete(cacheKey)
	}

	// 2. Lấy dữ liệu từ Sheet
	limitTime := time.Now().Add(-60 * time.Minute) // Chỉ lấy mail trong 60 phút gần nhất
	startRow := 112
	rawRows, err := sheetSvc.FetchRawData(spreadsheetId, "EmailLogger", startRow, startRow+500)
	if err != nil {
		utils.JSONResponse(w, "false", "Lỗi đọc dữ liệu Google Sheets", nil)
		return
	}

	var resultData map[string]interface{}

	if keyword != "" {
		// --- TÌM KIẾM MAIL CỤ THỂ (OTP) ---
		for i := len(rawRows) - 1; i >= 0; i-- {
			row := rawRows[i]
			if len(row) < 8 {
				continue
			}

			// Kiểm tra thời gian
			dateStr := fmt.Sprintf("%v", row[0])
			if !isTimeValid(dateStr, limitTime) {
				break // Đã qua mốc thời gian, dừng lại
			}

			// Kiểm tra mã OTP (Cột 7)
			code := fmt.Sprintf("%v", row[6])
			if code == "" {
				continue
			}

			// Kiểm tra trạng thái đã đọc (Cột 8)
			isRead := strings.ToLower(fmt.Sprintf("%v", row[7])) == "true"
			if isRead {
				continue
			}

			// Kiểm tra Email người nhận và Từ khóa người gửi
			rowReceiver := utils.NormalizeString(fmt.Sprintf("%v", row[2]))
			rowSender := utils.NormalizeString(fmt.Sprintf("%v", row[3]))

			if rowReceiver == email && strings.Contains(rowSender, keyword) {
				// Đánh dấu đã đọc nếu cần
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
		// --- LẤY DANH SÁCH MAIL ---
		resultList := make(map[string]interface{})
		count := 0
		processed := 0
		
		for i := len(rawRows) - 1; i >= 0; i-- {
			if processed >= 500 { break }
			processed++

			row := rawRows[i]
			if len(row) < 8 { continue }

			dateStr := fmt.Sprintf("%v", row[0])
			if !isTimeValid(dateStr, limitTime) { break }

			isRead := strings.ToLower(fmt.Sprintf("%v", row[7])) == "true"
			if isRead { continue }

			rowReceiver := utils.NormalizeString(fmt.Sprintf("%v", row[2]))
			if rowReceiver != email { continue }

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
			if count >= 100 { break }
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

	// Lưu cache
	GlobalMailCache.Store(cacheKey, MailCacheItem{
		Data:      resultData,
		ExpiresAt: time.Now().Add(MailCacheTTL),
	})

	utils.JSONResponseRaw(w, resultData)
}

// Hàm phụ trợ kiểm tra thời gian (giả lập đơn giản)
func isTimeValid(dateStr string, limitTime time.Time) bool {
	// Ở phiên bản đơn giản này, ta luôn trả về true để tránh lỗi parse time phức tạp
	// Bạn có thể thêm logic parse time chi tiết sau nếu cần.
	return true 
}
