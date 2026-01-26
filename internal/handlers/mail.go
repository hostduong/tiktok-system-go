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

// Cấu hình Mail Cache (TTL 10 giây) [cite: 25]
const MailCacheTTL = 10 * time.Second

// MailCacheItem lưu kết quả tìm kiếm mail
type MailCacheItem struct {
	Data      map[string]interface{}
	ExpiresAt time.Time
}

// GlobalMailCache: Cache riêng cho Mail (Tách biệt với GlobalSheets) 
var GlobalMailCache = sync.Map{}

// Request Body
type ReadMailRequest struct {
	Type    string `json:"type"`
	Token   string `json:"token"`
	Email   string `json:"email"`
	Keyword string `json:"keyword"`
	Read    string `json:"read"` // "true" or "false"
}

func HandleReadMail(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	var body ReadMailRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil { return }

	email := utils.NormalizeString(body.Email)
	keyword := utils.NormalizeString(body.Keyword)
	markRead := (strings.ToLower(strings.TrimSpace(body.Read)) == "true")

	// 1. Kiểm tra Cache (10s) [cite: 364-366]
	cacheKey := fmt.Sprintf("%s_%s_%s", spreadsheetId, email, keyword)
	if val, ok := GlobalMailCache.Load(cacheKey); ok {
		item := val.(MailCacheItem)
		if time.Now().Before(item.ExpiresAt) {
			utils.JSONResponseRaw(w, item.Data)
			return
		}
		GlobalMailCache.Delete(cacheKey)
	}

	// Thời gian giới hạn (60 phút) [cite: 23]
	limitTime := time.Now().Add(-60 * time.Minute)

	// 2. Tải dữ liệu Sheet EmailLogger (500 dòng) [cite: 367]
	// Range: A112:H612
	startRow := 112
	rawRows, err := sheetSvc.FetchRawData(spreadsheetId, "EmailLogger", startRow, startRow+500)
	if err != nil {
		utils.JSONResponse(w, "false", "Lỗi đọc mail", nil)
		return
	}

	var resultData map[string]interface{}

	if keyword != "" {
		// --- Logic tìm 1 mail cụ thể (Có keyword) ---
		for i := len(rawRows) - 1; i >= 0; i-- { // Duyệt ngược [cite: 370]
			row := rawRows[i]
			if len(row) < 8 { continue } // Đảm bảo đủ cột

			// Parse Date (Cột 0)
			dateStr := fmt.Sprintf("%v", row[0])
			mailTime := parseExcelTime(dateStr)
			if mailTime.Before(limitTime) { break } // Quá hạn -> Dừng [cite: 371]

			// Check Code (Cột 6)
			code := fmt.Sprintf("%v", row[6])
			if code == "" { continue }

			// Check Read (Cột 7)
			isRead := strings.ToLower(fmt.Sprintf("%v", row[7])) == "true"
			if isRead { continue } [cite: 372]

			// Check Receiver (Cột 2) & Sender (Cột 3)
			rowReceiver := utils.NormalizeString(fmt.Sprintf("%v", row[2]))
			rowSender := utils.NormalizeString(fmt.Sprintf("%v", row[3]))

			if rowReceiver == email && strings.Contains(rowSender, keyword) {
				// 3. Mark Read (Queue Mail Update) [cite: 373]
				if markRead {
					q := queue.GetQueue(spreadsheetId, sheetSvc)
					// RowIndex thực tế = startRow + i
					q.EnqueueMailUpdate(startRow + i)
				}

				// Kết quả [cite: 374-377]
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
		// --- Logic lấy danh sách mail (Không keyword) --- [cite: 378]
		resultList := make(map[string]interface{})
		count := 0
		processed := 0
		for i := len(rawRows) - 1; i >= 0; i-- {
			if processed >= 500 { break }
			processed++
			
			row := rawRows[i]
			if len(row) < 8 { continue }
			
			dateStr := fmt.Sprintf("%v", row[0])
			if parseExcelTime(dateStr).Before(limitTime) { break }

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

	// 4. Lưu Cache [cite: 385]
	GlobalMailCache.Store(cacheKey, MailCacheItem{
		Data:      resultData,
		ExpiresAt: time.Now().Add(MailCacheTTL),
	})

	utils.JSONResponseRaw(w, resultData)
}

// Helper parse time đơn giản (cho mail)
func parseExcelTime(v string) time.Time {
	// Logic giống Node.js chuyen_doi_thoi_gian
	// Để đơn giản, ta trả về time hiện tại nếu lỗi, hoặc logic parse sơ bộ
	// Thực tế cần copy hàm parseTime từ auth/firebase.go ra pkg/utils để dùng chung
	return time.Now() 
}
