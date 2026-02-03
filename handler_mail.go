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

	// Kích hoạt dọn dẹp nếu có log mail
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

	// Đánh dấu đã đọc -> Cập nhật RAM nóng
	if found && markRead {
		STATE.SheetMutex.Lock()
		// Kiểm tra lại index vì có thể RAM vừa bị cắt bởi luồng Cleanup
		if targetIdx < len(cacheData.RawValues) {
			cacheData.RawValues[targetIdx][7] = "TRUE"
			if targetIdx < len(cacheData.CleanValues) && 7 < len(cacheData.CleanValues[targetIdx]) {
				cacheData.CleanValues[targetIdx][7] = "true"
			}
			
			// Copy dữ liệu để ghi xuống Disk
			rowToUpdate := make([]interface{}, len(cacheData.RawValues[targetIdx]))
			copy(rowToUpdate, cacheData.RawValues[targetIdx])
			STATE.SheetMutex.Unlock()
			
			// Ghi xuống Disk (Queue)
			QueueUpdate(sid, SHEET_NAMES.EMAIL_LOGGER, targetIdx, rowToUpdate)
		} else {
			STATE.SheetMutex.Unlock()
		}
	}

	if found {
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "messenger": "Lấy mã thành công", "email": resultData})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "messenger": "Không tìm thấy mail", "email": map[string]interface{}{}})
	}
}

// =================================================================================================
// 🟢 LOGIC DỌN DẸP THÔNG MINH (SMART CLEANUP - DISK & RAM TRIM)
// =================================================================================================
func CleanupOldMails(sid string) {
	STATE.SheetMutex.Lock() // 🔒 Khóa Ghi ngay từ đầu để tính toán chính xác
	cacheKey := sid + KEY_SEPARATOR + SHEET_NAMES.EMAIL_LOGGER
	cached, exists := STATE.SheetCache[cacheKey]
	
	if !exists { 
		STATE.SheetMutex.Unlock()
		return 
	}

	// 1. Kiểm tra ngưỡng trong RAM
	// Lưu ý: cached.RawValues là dữ liệu trong RAM (tương ứng dòng 112 trở đi)
	ramCount := len(cached.RawValues)
	
	// Tổng dòng thực tế = Dòng bắt đầu (11) + Số dòng trong RAM
	// Tuy nhiên RANGES.MAX_ROW_CLEAN (1112) là số dòng tuyệt đối trong Excel.
	// RANGES.EMAIL_START_ROW (112).
	// Vậy ngưỡng kích hoạt tính theo độ dài RAM là: 1112 - 112 = 1000 dòng.
	thresholdRAM := RANGES.MAX_ROW_CLEAN - RANGES.EMAIL_START_ROW

	if ramCount > thresholdRAM {
		// Log
		log.Printf("🧹 [CLEANUP] RAM Email vượt ngưỡng (%d dòng). Tiến hành cắt %d dòng...", ramCount, RANGES.DELETE_COUNT)

		// 2. Cắt dữ liệu trong RAM (Slicing) - Giữ lại phần đuôi
		if ramCount > RANGES.DELETE_COUNT {
			cached.RawValues = cached.RawValues[RANGES.DELETE_COUNT:]
			cached.CleanValues = cached.CleanValues[RANGES.DELETE_COUNT:]
		} else {
			// Trường hợp hiếm: Xóa sạch nếu số lượng xóa >= số lượng có
			cached.RawValues = [][]interface{}{}
			cached.CleanValues = [][]string{}
		}

		STATE.SheetMutex.Unlock() // 🔓 Mở khóa RAM để hệ thống chạy tiếp

		// 3. Gọi Google API để xóa trên Disk (Chạy bất đồng bộ bên ngoài Lock)
		startIndex := int64(RANGES.EMAIL_START_ROW - 1)
		endIndex := startIndex + int64(RANGES.DELETE_COUNT)

		req := &sheets.BatchUpdateSpreadsheetRequest{
			Requests: []*sheets.Request{
				{
					DeleteDimension: &sheets.DeleteDimensionRequest{
						Range: &sheets.DimensionRange{
							SheetId:   getSheetIdByName(sid, SHEET_NAMES.EMAIL_LOGGER),
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
			log.Printf("❌ [CLEANUP ERROR] Lỗi xóa Google Sheet: %v", err)
			// Nếu xóa Disk lỗi nhưng RAM đã xóa -> Lần sau reload sẽ lại thấy cũ -> Chấp nhận được
		} else {
			log.Println("✅ [CLEANUP SUCCESS] Đã đồng bộ xóa trên Disk.")
		}
	} else {
		STATE.SheetMutex.Unlock()
	}
}

func getSheetIdByName(spreadsheetId, sheetName string) int64 {
	resp, err := sheetsService.Spreadsheets.Get(spreadsheetId).Do()
	if err != nil { return 0 }
	for _, sheet := range resp.Sheets {
		if sheet.Properties.Title == sheetName {
			return sheet.Properties.SheetId
		}
	}
	return 0
}
