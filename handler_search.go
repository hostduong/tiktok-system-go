package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings" // Import strings để dùng HasPrefix
	"time"    // Import time để dùng Now
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
	criteriaMatch := make(map[int][]string)
	criteriaContains := make(map[int][]string)
	criteriaMin := make(map[int]float64)
	criteriaMax := make(map[int]float64)
	criteriaTime := make(map[int]float64)
	
	// Parse input từ body
	for k, v := range body {
		if strings.HasPrefix(k, "match_col_") {
			idx, _ := strconv.Atoi(k[10:])
			if arr := parseConditionInput(v); len(arr) > 0 { criteriaMatch[idx] = arr }
		} else if strings.HasPrefix(k, "contains_col_") {
			idx, _ := strconv.Atoi(k[13:])
			if arr := parseConditionInput(v); len(arr) > 0 { criteriaContains[idx] = arr }
		} else if strings.HasPrefix(k, "min_col_") {
			idx, _ := strconv.Atoi(k[8:])
			if val, ok := v.(float64); ok { criteriaMin[idx] = val }
		} else if strings.HasPrefix(k, "max_col_") {
			idx, _ := strconv.Atoi(k[8:])
			if val, ok := v.(float64); ok { criteriaMax[idx] = val }
		} else if strings.HasPrefix(k, "last_hours_col_") {
			idx, _ := strconv.Atoi(k[15:])
			if val, ok := v.(float64); ok { criteriaTime[idx] = val }
		}
	}

	// Lấy limit
	limit := 1000
	if l, ok := body["limit"]; ok { 
		if val, ok := l.(float64); ok && val > 0 { limit = int(val) }
	}

	result := make(map[int]map[string]interface{})
	count := 0

	cache.Mutex.RLock()
	now := time.Now().UnixMilli() // Đã được sử dụng ở logic bên dưới
	
	for i, row := range cache.RawValues {
		if count >= limit { break }
		match := true
		
		// 1. Check Match
		for idx, arr := range criteriaMatch {
			cellVal := ""
			if idx < CACHE.CLEAN_COL_LIMIT {
				cellVal = cache.CleanValues[i][idx]
			} else {
				cellVal = CleanString(row[idx])
			}
			found := false
			for _, target := range arr { if target == cellVal { found = true; break } }
			if !found { match = false; break }
		}
		if !match { continue }

		// 2. Check Contains
		for idx, arr := range criteriaContains {
			cellVal := CleanString(row[idx])
			found := false
			for _, target := range arr { if strings.Contains(cellVal, target) { found = true; break } }
			if !found { match = false; break }
		}
		if !match { continue }

		// 3. Check Min/Max
		for idx, minVal := range criteriaMin {
			if val, ok := toFloat(row[idx]); !ok || val < minVal { match = false; break }
		}
		if !match { continue }
		for idx, maxVal := range criteriaMax {
			if val, ok := toFloat(row[idx]); !ok || val > maxVal { match = false; break }
		}
		if !match { continue }

		// 4. Check Time (Last Hours) -> Sử dụng biến `now`
		for idx, hours := range criteriaTime {
			timeVal := ConvertSerialDate(row[idx])
			if timeVal == 0 { match = false; break }
			diffHours := float64(now - timeVal) / 3600000.0
			if diffHours > hours { match = false; break }
		}
		if !match { continue }
		
		// Match Success
		item := make(map[string]interface{})
		item["row_index"] = i + RANGES.DATA_START_ROW
		result[count] = item
		count++
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

// Helper parsing
func parseConditionInput(v interface{}) []string {
	if s, ok := v.(string); ok {
		return []string{CleanString(s)}
	}
	if arr, ok := v.([]interface{}); ok {
		res := []string{}
		for _, item := range arr {
			res = append(res, CleanString(item))
		}
		return res
	}
	return []string{}
}

func toFloat(v interface{}) (float64, bool) {
	if f, ok := v.(float64); ok { return f, true }
	return 0, false
}
