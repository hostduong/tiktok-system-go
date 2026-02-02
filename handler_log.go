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

1. TÍNH NĂNG MỚI (AUTO TIME):
   - Nếu nội dung cột có chứa ký tự "{{time}}", Server sẽ tự động thay thế bằng thời gian hiện tại.
   - Định dạng: dd/mm/yyyy HH:MM:SS (Múi giờ Việt Nam UTC+7).
   - Ví dụ: Client gửi "Lỗi lúc {{time}}" -> Server ghi "Lỗi lúc 23/12/2025 11:10:39".

2. CẤU TRÚC BODY REQUEST:
{
  "token": "...",
  "data": [
    {
      "sheet": "PostLogger",
      "action": "cache",        // cache = Ghi nóng RAM (Search thấy ngay)
      "col_0": "{{time}}",      // <--- Server tự điền giờ vào đây
      "col_1": "user_abc",
      "col_2": "Đã login {{time}}" // <--- Có thể chèn vào giữa câu
    }
  ]
}
*/

const (
	TIME_MARKER = "{{time}}"            // Ký tự đánh dấu
	TIME_LAYOUT = "02/01/2006 15:04:05" // Định dạng giờ: dd/mm/yyyy HH:MM:SS
)

func HandleLogData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi Body JSON"}`, 400)
		return
	}

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		http.Error(w, `{"status":"false","messenger":"Lỗi xác thực"}`, 401)
		return
	}

	dataList, _ := body["data"].([]interface{})
	if len(dataList) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Không có dữ liệu"})
		return
	}

	rowsBySheet := make(map[string][][]interface{})
	
	// Chuẩn bị thời gian hiện tại (Múi giờ Việt Nam UTC+7)
	// Tính 1 lần dùng chung cho cả batch request để đồng bộ
	vnZone := time.FixedZone("UTC+7", 7*3600)
	nowStr := time.Now().In(vnZone).Format(TIME_LAYOUT)

	// 2. Duyệt từng dòng log
	for _, item := range dataList {
		obj, ok := item.(map[string]interface{})
		if !ok { continue }

		// A. Validate Sheet
		sheetName := SHEET_NAMES.POST_LOGGER
		if s, ok := obj["sheet"].(string); ok && s != "" {
			if isValidSheetName(s) { sheetName = s }
		}

		// B. Validate Action
		action := "sheet"
		if a, ok := obj["action"].(string); ok && a != "" { action = strings.ToLower(a) }

		// C. Xây dựng Row
		maxCol := 0
		for k := range obj {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(k[4:])
				if idx > maxCol { maxCol = idx }
			}
		}
		if maxCol > 100 { maxCol = 100 }

		row := make([]interface{}, maxCol+1)
		for i := range row { row[i] = "" }

		for k, v := range obj {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(k[4:])
				if idx <= maxCol {
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

		rowsBySheet[sheetName] = append(rowsBySheet[sheetName], row)

		// D. Xử lý Cache Real-time
		if strings.Contains(action, "cache") {
			updateRamCache(tokenData.SpreadsheetID, sheetName, row)
		}
	}

	// 3. Đẩy xuống Queue
	for sheet, rows := range rowsBySheet {
		if len(rows) > 0 {
			QueueAppend(tokenData.SpreadsheetID, sheet, rows)
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Đã ghi log thành công"})
}

// =================================================================================================
// 🟢 CÁC HÀM HỖ TRỢ
// =================================================================================================

func updateRamCache(sid, sheetName string, row []interface{}) {
	cacheKey := sid + KEY_SEPARATOR + sheetName

	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	if cached, exists := STATE.SheetCache[cacheKey]; exists {
		cached.RawValues = append(cached.RawValues, row)

		cleanRow := make([]string, CACHE.CLEAN_COL_LIMIT)
		for i, val := range row {
			if i < CACHE.CLEAN_COL_LIMIT {
				cleanRow[i] = CleanString(val)
			}
		}
		cached.CleanValues = append(cached.CleanValues, cleanRow)
	}
}

func isValidSheetName(name string) bool {
	switch name {
	case SHEET_NAMES.POST_LOGGER, 
		 SHEET_NAMES.EMAIL_LOGGER, 
		 SHEET_NAMES.ERROR_LOGGER, 
		 SHEET_NAMES.DATA_TIKTOK, 
		 SHEET_NAMES.USER_NAME:
		return true
	default:
		return false
	}
}
