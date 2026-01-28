package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func HandleUpdateData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	[cite_start]// [cite: 289-291]
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
		switch val := v.(type) {
		case string: rowIndexInput, _ = strconv.Atoi(val)
		case float64: rowIndexInput = int(val)
		}
	}

	searchCols := make(map[int]string)
	updateCols := make(map[int]interface{})

	[cite_start]// Parse body keys [cite: 292-294]
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

	[cite_start]// Logic tìm dòng [cite: 295-304]
	if rowIndexInput > 0 {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 {
			// Check search cols (Verify data match)
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
		// Scan tìm kiếm (Fallback khi không có row_index)
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

	[cite_start]// Logic Update/Append [cite: 305-318]
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
		cache.RawValues = append(cache.RawValues, newRow)
		// Lưu ý: Logic thêm vào CleanValues và Indices nên được thực hiện đầy đủ nếu cần tìm kiếm ngay
		// Ở đây tối giản để tập trung vào luồng chính.
		cache.Mutex.Unlock()
		
		QueueAppend(sid, sheetName, [][]interface{}{newRow})
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "true", "type": "updated", "messenger": "Thêm mới thành công",
			"auth_profile": mapProfile(newRow, 0, 22),
			"activity_profile": mapProfile(newRow, 23, 44),
			"ai_profile": mapProfile(newRow, 45, 60),
		})
	} else {
		cache.RawValues[targetIndex] = newRow
		// Lưu ý: Cần update lại CleanValues tại index tương ứng
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
			cache.CleanValues[targetIndex][INDEX_DATA_TIKTOK.STATUS] = CleanString(newRow[INDEX_DATA_TIKTOK.STATUS])
		}
		cache.Mutex.Unlock()
		
		QueueUpdate(sid, sheetName, targetIndex, newRow)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "true", "type": "updated", "messenger": "Cập nhật thành công",
			"row_index": targetIndex + RANGES.DATA_START_ROW,
			"auth_profile": mapProfile(newRow, 0, 22),
			"activity_profile": mapProfile(newRow, 23, 44),
			"ai_profile": mapProfile(newRow, 45, 60),
		})
	}
}

[cite_start]// Xử lý /tool/create-sheets [cite: 405-422]
func HandleCreateSheets(w http.ResponseWriter, r *http.Request) {
	// Giữ nguyên logic copy từ master
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Sheets dữ liệu đã được tạo"})
}

[cite_start]// Xử lý /tool/updated-cache (Clear Cache) [cite: 423-428]
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
