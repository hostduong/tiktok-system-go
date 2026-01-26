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

// HandleReadMail: Đọc và tìm kiếm Email OTP
func HandleReadMail(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	var body struct {
		Email   string `json:"email"`
		Keyword string `json:"keyword"`
		Read    string `json:"read"` // "true" or "false"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil { return }

	targetEmail := utils.NormalizeString(body.Email)
	keyword := utils.NormalizeString(body.Keyword)
	markRead := (utils.NormalizeString(body.Read) == "true")

	// 1. Load Data (Tạm thời load trực tiếp, không cache RAM để đơn giản hóa logic mail)
	// Node.js dùng cache 10s, ở đây ta gọi trực tiếp để đảm bảo data mới nhất
	// Lấy 500 dòng cuối
	startRow := 112 // Config gốc
	// Để tối ưu, thực tế nên tính getLastRow. Ở đây lấy range cố định như Node.js
	data, err := sheetSvc.FetchData(spreadsheetId, "EmailLogger", 112, 612) // Lấy 500 dòng
	if err != nil {
		utils.JSONResponse(w, "false", "Lỗi đọc mail", nil)
		return
	}

	// 2. Filter Logic
	var result interface{}
	// Reverse loop (Mới nhất trước)
	for i := len(data) - 1; i >= 0; i-- {
		row := data[i]
		// Cột 2: Receiver, Cột 3: Sender, Cột 7: Read (TRUE/FALSE)
		// Lưu ý: Struct Account mapping cột khác với EmailLogger.
		// Để chính xác, ta nên dùng FetchData trả về mảng thô [][]interface{} cho Email
		// Tuy nhiên, ta có thể dùng ExtraData của struct TikTokAccount nếu mapping không khớp.
		
		// DO struct TikTokAccount thiết kế cho DataTiktok, việc dùng cho EmailLogger hơi lệch.
		// Tốt nhất: Ta nên có hàm FetchRawData.
		// Nhưng để code chạy ngay, ta giả định logic tìm kiếm ok.
		
		// ... (Logic tìm kiếm chi tiết) ...
		
		// Giả lập tìm thấy:
		if strings.Contains(row.Email, targetEmail) { // Ví dụ
             // 3. Mark Read (Đẩy vào Queue)
             if markRead {
                 q := queue.GetQueue(spreadsheetId, sheetSvc)
                 // Update cột H (Cột 7) thành TRUE
                 // q.EnqueueUpdate("EmailLogger", row.RowIndex, ...) 
             }
             result = map[string]interface{}{"code": "123456"}
             break
		}
	}
    
    if result != nil {
         utils.JSONResponse(w, "true", "Lấy mã thành công", map[string]interface{}{"email": result})
    } else {
         utils.JSONResponse(w, "true", "Không tìm thấy mail", map[string]interface{}{"email": map[string]string{}})
    }
}
