package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func HandleSearchData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	token, _ := body["token"].(string)
	auth := CheckToken(token)
	if !auth.IsValid {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": auth.Messenger})
		return
	}

	sheetName, _ := body["sheet"].(string)
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	
	cache, _ := LayDuLieu(auth.SpreadsheetID, sheetName, false)
	
	// Parse Criteria
	// match_col, contains_col, min_col, max_col, last_hours_col
	// ... (Logic parsing giống Node.js, duyệt map body)
	
	result := make(map[int]map[string]interface{})
	count := 0
	limit := 1000 // Default limit logic
	if l, ok := body["limit"]; ok { limit = int(l.(float64)) }

	cache.Mutex.RLock()
	now := time.Now().UnixMilli()
	
	for i, row := range cache.RawValues {
		if count >= limit { break }
		match := true
		
		// Demo 1 điều kiện: Match Col (Các điều kiện khác tương tự)
		// Cần loop qua body để check match_col_X
		for k, v := range body {
			if strings.HasPrefix(k, "match_col_") {
				colIdx, _ := strconv.Atoi(k[10:])
				target := CleanString(v)
				
				cellVal := ""
				if colIdx < CACHE.CLEAN_COL_LIMIT {
					cellVal = cache.CleanValues[i][colIdx]
				} else {
					cellVal = CleanString(row[colIdx])
				}
				
				if cellVal != target { match = false; break }
			}
			// Add logic for contains, min, max, time...
		}
		
		if match {
			item := make(map[string]interface{})
			item["row_index"] = i + RANGES.DATA_START_ROW
			// Add return cols if requested...
			result[count] = item
			count++
		}
	}
	cache.Mutex.RUnlock()
	
	if count == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Không tìm thấy dữ liệu"})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "true", "messenger": "Lấy dữ liệu thành công", "data": result,
		})
	}
}
