package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

/*
=================================================================================================
📘 TÀI LIỆU API: GHI LOG & DỮ LIỆU (POST /tool/log)
=================================================================================================

1. MỤC ĐÍCH:
   - Ghi lại nhật ký hoạt động, lỗi, hoặc dữ liệu mới (như OTP, Email) vào Google Sheets.
   - Hỗ trợ cơ chế "Write-Behind" (Trả về ngay, ghi sau) để không làm chậm Tool.
   - Hỗ trợ ghi nóng vào RAM (Cache) để các tool khác có thể Search thấy ngay lập tức (dùng cho OTP).

2. TÍNH NĂNG TỰ ĐỘNG (AUTO MACROS):
   - {{time}} : Tự động thay thế bằng thời gian hiện tại (dd/mm/yyyy HH:MM:SS - Múi giờ VN).

3. CẤU TRÚC BODY REQUEST:
{
  "token": "...",
  "data": [
    {
      "sheet": "PostLogger",    // Tên sheet cần ghi (Mặc định: PostLogger)
      "action": "cache",        // "sheet" (Ghi đĩa) hoặc "cache" (Ghi đĩa + RAM nóng)
      
      // Dữ liệu các cột (col_X)
      "col_0": "{{time}}",      // Cột A: Tự điền giờ
      "col_1": "user_abc",      // Cột B: Tên User
      "col_2": "Login OK",      // Cột C: Nội dung
      "col_6": "123456"         // Cột G: Mã OTP (Ví dụ)
    },
    { ... } // Hỗ trợ ghi nhiều dòng 1 lúc
  ]
}

4. CẤU TRÚC RESPONSE:
{
    "status": "true",
    "messenger": "Đã ghi log thành công"
}
*/

const (
	TIME_MARKER = "{{time}}"            // Ký tự đánh dấu để thay thế giờ
	TIME_LAYOUT = "02/01/2006 15:04:05" // Định dạng giờ: dd/mm/yyyy HH:MM:SS
)

// =================================================================================================
// 🟢 HANDLER CHÍNH
// =================================================================================================

func HandleLogData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi cấu trúc JSON"}`, 400)
		return
	}

	// 2. Xác thực Token
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return } // Middleware đã xử lý lỗi 401

	dataList, _ := body["data"].([]interface{})
	if len(dataList) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Không có dữ liệu log"})
		return
	}

	rowsBySheet := make(map[string][][]interface{})
	
	// Chuẩn bị thời gian hiện tại (Múi giờ Việt Nam UTC+7)
	// Tính 1 lần dùng chung cho cả batch request để đồng bộ
	vnZone := time.FixedZone("UTC+7", 7*3600)
	nowStr := time.Now().In(vnZone).Format(TIME_LAYOUT)

	// 3. Duyệt và Xử lý từng dòng log
	for _, item := range dataList {
		obj, ok := item.(map[string]interface{})
		if !ok { continue }

		// A. Validate Sheet Name (Chống ghi bậy vào sheet lung tung)
		sheetName := SHEET_NAMES.POST_LOGGER
		if s, ok := obj["sheet"].(string); ok && s != "" {
			if isValidSheetName(s) { sheetName = s }
		}

		// B. Xác định hành động (Ghi thường hay Ghi nóng)
		action := "sheet"
		if a, ok := obj["action"].(string); ok && a != "" { action = strings.ToLower(a) }

		// C. Tìm cột lớn nhất để khởi tạo mảng
		maxCol := 0
		for k := range obj {
			if strings.HasPrefix(k, "col_") {
				if idx, err := strconv.Atoi(k[4:]); err == nil { // Chỉ nhận col_ số nguyên
					if idx > maxCol { maxCol = idx }
				}
			}
		}
		// Giới hạn cột an toàn (Tránh user gửi col_9999 gây tràn bộ nhớ)
		if maxCol > 100 { maxCol = 100 }

		// D. Xây dựng dòng dữ liệu (Row)
		row := make([]interface{}, maxCol+1)
		for i := range row { row[i] = "" } // Init rỗng

		for k, v := range obj {
			if strings.HasPrefix(k, "col_") {
				if idx, err := strconv.Atoi(k[4:]); err == nil && idx <= maxCol {
					// 🔥 LOGIC AUTO TIME REPLACEMENT 🔥
					val := v
					if strVal, ok := v.(string); ok {
						if strings.Contains(strVal, TIME_MARKER) {
							// Thay thế {{time}} bằng giờ hiện tại
							val = strings.ReplaceAll(strVal, TIME_MARKER, nowStr)
						}
					}
					row[idx] = val
				}
			}
		}

		// Gom nhóm theo Sheet để Batch Write
		rowsBySheet[sheetName] = append(rowsBySheet[sheetName], row)

		// E. Xử lý Cache Real-time (Nếu action="cache")
		// Dùng cho OTP/Email để Search thấy ngay lập tức
		if strings.Contains(action, "cache") {
			updateRamCache(tokenData.SpreadsheetID, sheetName, row)
		}
	}

	// 4. Đẩy xuống Queue ghi đĩa
	for sheet, rows := range rowsBySheet {
		if len(rows) > 0 {
			QueueAppend(tokenData.SpreadsheetID, sheet, rows)
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Đã ghi log thành công"})
}

// =================================================================================================
// 🛠️ CÁC HÀM HỖ TRỢ (HELPER FUNCTIONS)
// =================================================================================================

// Cập nhật nóng vào RAM (Thread-Safe)
func updateRamCache(sid, sheetName string, row []interface{}) {
	cacheKey := sid + KEY_SEPARATOR + sheetName

	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	if cached, exists := STATE.SheetCache[cacheKey]; exists {
		// 1. Append dữ liệu thô
		cached.RawValues = append(cached.RawValues, row)

		// 2. Append dữ liệu sạch (CleanValues) để phục vụ Search
		cleanRow := make([]string, CACHE.CLEAN_COL_LIMIT)
		for i, val := range row {
			if i < CACHE.CLEAN_COL_LIMIT {
				cleanRow[i] = CleanString(val)
			}
		}
		cached.CleanValues = append(cached.CleanValues, cleanRow)
		
		// 3. Cập nhật thời gian truy cập (Để tránh bị dọn dẹp nhầm)
		cached.LastAccessed = time.Now().UnixMilli()
	}
}

// Danh sách các Sheet cho phép ghi Log
func isValidSheetName(name string) bool {
	switch name {
	case SHEET_NAMES.POST_LOGGER, 
		 SHEET_NAMES.EMAIL_LOGGER, 
		 SHEET_NAMES.ERROR_LOGGER, 
		 SHEET_NAMES.DATA_TIKTOK, // Cho phép ghi thêm nick mới
		 SHEET_NAMES.USER_NAME:
		return true
	default:
		return false
	}
}
