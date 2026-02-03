package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/sheets/v4"
)

// =================================================================================================
// 🟢 API 1: GHI LOG MAIL (POST /tool/mail_log)
// =================================================================================================
func HandleMailData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	dataList, _ := body["data"].([]interface{})
	rowsBySheet := make(map[string][][]interface{})
	hasEmailLog := false

	for _, item := range dataList {
		obj, ok := item.(map[string]interface{})
		if !ok { continue }
		sheet := SHEET_NAMES.EMAIL_LOGGER
		if s, ok := obj["sheet"].(string); ok && s != "" { 
			sheet = s 
		}
		
		// Đánh dấu nếu có ghi vào EmailLogger để kích hoạt dọn dẹp
		if sheet == SHEET_NAMES.EMAIL_LOGGER {
			hasEmailLog = true
		}

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

	// 🔥 KÍCH HOẠT DỌN DẸP (Chạy ngầm)
	if hasEmailLog {
		go CleanupOldMails(tokenData.SpreadsheetID)
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Đã tiếp nhận mail log"})
}

// =================================================================================================
// 🟢 API 2: ĐỌC MAIL/OTP (POST /tool/read_mail)
// =================================================================================================
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
	
	// Quét ngược
	for i := len(rows) - 1; i >= 0; i-- {
		if processCount >= RANGES.EMAIL_LIMIT_ROWS { break }
		processCount++
		
		row := rows[i]
		if len(row) <= 7 { continue }

		mailTime := ConvertSerialDate(row[0]) 
		if mailTime < limitTime { break }

		if fmt.Sprintf("%v", row[6]) == "" { continue }
		isRead := CleanString(row[7])
		if isRead == "true" { continue }
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
		STATE.SheetMutex.Lock()
		if targetIdx < len(cacheData.RawValues) {
			cacheData.RawValues[targetIdx][7] = "TRUE"
			if targetIdx < len(cacheData.CleanValues) && 7 < len(cacheData.CleanValues[targetIdx]) {
				cacheData.CleanValues[targetIdx][7] = "true"
			}
		}
		rowToUpdate := make([]interface{}, len(cacheData.RawValues[targetIdx]))
		copy(rowToUpdate, cacheData.RawValues[targetIdx])
		STATE.SheetMutex.Unlock()
		
		QueueUpdate(sid, SHEET_NAMES.EMAIL_LOGGER, targetIdx, rowToUpdate)
	}

	if found {
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "messenger": "Lấy mã thành công", "email": resultData})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "messenger": "Không tìm thấy mail", "email": map[string]interface{}{}})
	}
}

// =================================================================================================
// 🟢 LOGIC DỌN DẸP MAIL CŨ (AUTO CLEANUP)
// =================================================================================================
func CleanupOldMails(sid string) {
	// 1. Kiểm tra Cache để biết số lượng dòng hiện tại
	// (Không dùng Lock để tránh block các tiến trình khác, chỉ đọc nhanh)
	STATE.SheetMutex.RLock()
	cacheKey := sid + KEY_SEPARATOR + SHEET_NAMES.EMAIL_LOGGER
	cached, exists := STATE.SheetCache[cacheKey]
	STATE.SheetMutex.RUnlock()

	if !exists { return } // Nếu chưa có cache thì chưa cần dọn (hoặc để lần sau load sẽ biết)

	// Tính tổng số dòng thực tế trên Sheet
	// RANGES.DATA_START_ROW (11) + Số dòng trong Cache
	currentTotalRows := RANGES.DATA_START_ROW + len(cached.RawValues)

	// 2. Kiểm tra ngưỡng cấu hình (1112 dòng)
	if currentTotalRows > RANGES.MAX_ROW_CLEAN {
		// Log báo hiệu
		log.Printf("🧹 [CLEANUP] EmailLogger quá đầy (%d dòng). Đang xóa %d dòng cũ...", currentTotalRows, RANGES.DELETE_COUNT)

		// 3. Gọi Google API để xóa hàng (DeleteDimension)
		// API dùng Index bắt đầu từ 0.
		// Sheet Row 112 => Index 111.
		startIndex := int64(RANGES.EMAIL_START_ROW - 1) 
		endIndex := startIndex + int64(RANGES.DELETE_COUNT)

		req := &sheets.BatchUpdateSpreadsheetRequest{
			Requests: []*sheets.Request{
				{
					DeleteDimension: &sheets.DeleteDimensionRequest{
						Range: &sheets.DimensionRange{
							SheetId:   getSheetIdByName(sid, SHEET_NAMES.EMAIL_LOGGER), // Cần hàm lấy ID số của Sheet
							Dimension: "ROWS",
							StartIndex: startIndex,
							EndIndex:   endIndex,
						},
					},
				},
			},
		}

		_, err := sheetsService.Spreadsheets.BatchUpdate(sid, req).Do()
		if err != nil {
			log.Printf("❌ [CLEANUP ERROR] Không thể xóa dòng: %v", err)
			return
		}

		// 4. QUAN TRỌNG: Hủy Cache ngay lập tức
		// Để lần đọc tiếp theo Server buộc phải tải lại dữ liệu mới (đã bị cắt ngắn) từ Google
		STATE.SheetMutex.Lock()
		delete(STATE.SheetCache, cacheKey)
		STATE.SheetMutex.Unlock()
		
		log.Println("✅ [CLEANUP SUCCESS] Đã dọn dẹp và reset cache EmailLogger.")
	}
}

// Hàm hỗ trợ lấy SheetID (Dạng số) từ Tên Sheet (String)
// Google API xóa dòng yêu cầu SheetId số (VD: 0, 12345), không phải tên "EmailLogger"
func getSheetIdByName(spreadsheetId, sheetName string) int64 {
	resp, err := sheetsService.Spreadsheets.Get(spreadsheetId).Do()
	if err != nil { return 0 }
	
	for _, sheet := range resp.Sheets {
		if sheet.Properties.Title == sheetName {
			return sheet.Properties.SheetId
		}
	}
	return 0 // Mặc định về sheet đầu tiên nếu không tìm thấy
}
