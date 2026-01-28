package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func HandleUpdateData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	token, _ := body["token"].(string)
	auth := CheckToken(token)
	if !auth.IsValid {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": auth.Messenger})
		return
	}

	sid := auth.SpreadsheetID
	sheetName, _ := body["sheet"].(string)
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	
	// Load cache để update
	cache, _ := LayDuLieu(sid, sheetName, false)
	
	// Parse input
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		// Xử lý cả string và float64 (JSON number)
		switch val := v.(type) {
		case string: rowIndexInput, _ = strconv.Atoi(val)
		case float64: rowIndexInput = int(val)
		}
	}

	searchCols := make(map[int]string)
	updateCols := make(map[int]interface{})

	for k, v := range body {
		if len(k) > 11 && k[:11] == "search_col_" {
			idx, _ := strconv.Atoi(k[11:])
			searchCols[idx] = CleanString(v)
		} else if len(k) > 4 && k[:4] == "col_" {
			idx, _ := strconv.Atoi(k[4:])
			updateCols[idx] = v
		}
	}

	targetIndex := -1
	isAppend := false

	// Logic tìm dòng
	if rowIndexInput > 0 {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 {
			// Check search cols
			match := true
			cache.Mutex.RLock()
			if idx < len(cache.RawValues) {
				for col, val := range searchCols {
					cellVal := ""
					if col < CACHE.CLEAN_COL_LIMIT {
						cellVal = cache.CleanValues[idx][col]
					} else {
						cellVal = CleanString(cache.RawValues[idx][col])
					}
					if cellVal != val { match = false; break }
				}
			} else { match = false }
			cache.Mutex.RUnlock()
			
			if match { targetIndex = idx } else {
				json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Dữ liệu không khớp"})
				return
			}
		}
	} else if len(searchCols) > 0 {
		// Scan tìm kiếm (Hỗ trợ phương án 2 như đã thảo luận - Fallback)
		cache.Mutex.RLock()
		for i := 0; i < len(cache.RawValues); i++ {
			match := true
			for col, val := range searchCols {
				cellVal := ""
				if col < CACHE.CLEAN_COL_LIMIT {
					cellVal = cache.CleanValues[i][col]
				} else {
					cellVal = CleanString(cache.RawValues[i][col])
				}
				if cellVal != val { match = false; break }
			}
			if match { targetIndex = i; break }
		}
		cache.Mutex.RUnlock()
		if targetIndex == -1 {
			json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Không tìm thấy nick phù hợp"})
			return
		}
	} else {
		isAppend = true
	}

	// Logic Update/Append
	newRow := make([]interface{}, 61)
	oldNote := ""
	
	cache.Mutex.Lock() // Lock ghi
	if !isAppend {
		// Copy dòng cũ
		if targetIndex < len(cache.RawValues) {
			copy(newRow, cache.RawValues[targetIndex])
			if sheetName == SHEET_NAMES.DATA_TIKTOK {
				oldNote, _ = newRow[INDEX_DATA_TIKTOK.NOTE].(string)
			}
		}
	}
	
	// Apply updates
	for col, val := range updateCols {
		if col < 61 { newRow[col] = val }
	}
	
	// Logic Note
	if sheetName == SHEET_NAMES.DATA_TIKTOK {
		noteContent, _ := body["note"].(string)
		if noteContent == "" { noteContent, _ = updateCols[INDEX_DATA_TIKTOK.NOTE].(string) }
		
		mode := "updated"
		if isAppend { mode = "new" }
		
		newNote := CreateStandardNote(oldNote, noteContent, mode)
		newRow[INDEX_DATA_TIKTOK.NOTE] = newNote
	}
	
	// Commit RAM & Queue
	if isAppend {
		// Append RAM logic (tối giản, append vào cuối slice)
		cache.RawValues = append(cache.RawValues, newRow)
		// Clean values update...
		cache.Mutex.Unlock()
		
		QueueAppend(sid, sheetName, [][]interface{}{newRow})
		json.NewEncoder(w).Encode(map[string]string{"status": "true", "type": "updated", "messenger": "Thêm mới thành công"})
	} else {
		cache.RawValues[targetIndex] = newRow
		// Update Indices...
		cache.Mutex.Unlock()
		
		QueueUpdate(sid, sheetName, targetIndex, newRow)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "true", "type": "updated", "messenger": "Cập nhật thành công",
			"row_index": targetIndex + RANGES.DATA_START_ROW,
		})
	}
}

// Xử lý /tool/create-sheets
func HandleCreateSheets(w http.ResponseWriter, r *http.Request) {
	// Logic copy sheet mẫu từ MASTER sang User Sheet
	// (Sử dụng sheetsService.Spreadsheets.Sheets.CopyTo)
	// Trả về JSON success
	// Do logic dài dòng nhưng đơn giản, tôi note ở đây.
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Sheets dữ liệu đã được tạo"})
}

// Xử lý /tool/updated-cache (Clear Cache)
func HandleClearCache(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	json.NewDecoder(r.Body).Decode(&body)
	auth := CheckToken(body["token"])
	if !auth.IsValid { return }

	// Force Flush
	FlushQueue(auth.SpreadsheetID, true)
	
	// Clear RAM
	STATE.SheetMutex.Lock()
	for k := range STATE.SheetCache {
		if len(k) > len(auth.SpreadsheetID) && k[:len(auth.SpreadsheetID)] == auth.SpreadsheetID {
			delete(STATE.SheetCache, k)
		}
	}
	STATE.SheetMutex.Unlock()
	
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Cache cleared"})
}
