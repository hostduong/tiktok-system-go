package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func HandleSearchData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi Body JSON"}`, 400)
		return
	}

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }

	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Lỗi tải dữ liệu"})
		return
	}

	criteriaMatch := make(map[int][]string)
	criteriaContains := make(map[int][]string)
	criteriaMin := make(map[int]float64)
	criteriaMax := make(map[int]float64)
	criteriaTime := make(map[int]float64)

	// Parse Body (Chỉ dùng col_)
	for k, v := range body {
		if strings.HasPrefix(k, "match_col_") {
			idx, _ := strconv.Atoi(k[10:])
			if arr := parseConditionInput(v); len(arr) > 0 { criteriaMatch[idx] = arr }
		} else if strings.HasPrefix(k, "contains_col_") {
			idx, _ := strconv.Atoi(k[13:])
			if arr := parseConditionInput(v); len(arr) > 0 { criteriaContains[idx] = arr }
		} else if strings.HasPrefix(k, "min_col_") {
			idx, _ := strconv.Atoi(k[8:])
			if val, ok := toFloat(v); ok { criteriaMin[idx] = val }
		} else if strings.HasPrefix(k, "max_col_") {
			idx, _ := strconv.Atoi(k[8:])
			if val, ok := toFloat(v); ok { criteriaMax[idx] = val }
		} else if strings.HasPrefix(k, "last_hours_col_") {
			idx, _ := strconv.Atoi(k[15:])
			if val, ok := toFloat(v); ok { criteriaTime[idx] = val }
		}
	}

	limit := 1000
	if l, ok := body["limit"]; ok {
		if val, ok := toFloat(l); ok && val > 0 { limit = int(val) }
	}

	result := make(map[int]map[string]interface{})
	count := 0

	// 🔥 GLOBAL LOCK
	STATE.SheetMutex.RLock()
	defer STATE.SheetMutex.RUnlock()

	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	now := time.Now().UnixMilli()

	for i, row := range rows {
		if count >= limit { break }
		match := true

		// 1. Match
		for idx, arr := range criteriaMatch {
			cellVal := ""
			if idx < len(cleanRows[i]) { cellVal = cleanRows[i][idx] }
			found := false
			for _, target := range arr { if target == cellVal { found = true; break } }
			if !found { match = false; break }
		}
		if !match { continue }

		// 2. Contains
		for idx, arr := range criteriaContains {
			cellVal := ""
			if idx < len(cleanRows[i]) { cellVal = cleanRows[i][idx] }
			found := false
			for _, target := range arr { if strings.Contains(cellVal, target) { found = true; break } }
			if !found { match = false; break }
		}
		if !match { continue }

		// 3. Min/Max
		for idx, minVal := range criteriaMin {
			if val, ok := getFloatVal(row, idx); !ok || val < minVal { match = false; break }
		}
		if !match { continue }
		for idx, maxVal := range criteriaMax {
			if val, ok := getFloatVal(row, idx); !ok || val > maxVal { match = false; break }
		}
		if !match { continue }

		// 4. Time
		for idx, hours := range criteriaTime {
			timeVal := int64(0)
			if idx < len(row) { timeVal = ConvertSerialDate(row[idx]) }
			if timeVal == 0 { match = false; break }
			if float64(now-timeVal)/3600000.0 > hours { match = false; break }
		}
		if !match { continue }

		item := make(map[string]interface{})
		item["row_index"] = i + RANGES.DATA_START_ROW
		result[count] = item
		count++
	}

	if count == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Không tìm thấy dữ liệu"})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "messenger": "Thành công", "data": result})
	}
}

func parseConditionInput(v interface{}) []string {
	if s, ok := v.(string); ok { return []string{CleanString(s)} }
	if arr, ok := v.([]interface{}); ok {
		res := []string{}
		for _, item := range arr { res = append(res, CleanString(item)) }
		return res
	}
	return []string{}
}
func toFloat(v interface{}) (float64, bool) {
	if f, ok := v.(float64); ok { return f, true }
	return 0, false
}
func getFloatVal(row []interface{}, idx int) (float64, bool) {
	if idx >= len(row) { return 0, false }
	if f, ok := row[idx].(float64); ok { return f, true }
	s := fmt.Sprintf("%v", row[idx])
	if f, err := strconv.ParseFloat(s, 64); err == nil { return f, true }
	return 0, false
}
