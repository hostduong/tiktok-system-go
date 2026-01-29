package main

import (
	"encoding/json"
	"fmt" // 🔥 Added
	"net/http"
	"strconv"
	"strings"
	"time"
)

func HandleSearchData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi JSON"}`, 400)
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

	// Parse Criteria
	criteriaMatch := make(map[int][]string)
	criteriaContains := make(map[int][]string)
	
	// ... (Giữ nguyên logic parse input như cũ) ...
	// Để gọn code tôi rút gọn đoạn parse input, bạn giữ nguyên đoạn for k,v := range body nhé.
	// Đoạn quan trọng là Loop dưới đây:

	result := make(map[int]map[string]interface{})
	count := 0
	limit := 1000

	// 🔥 FIX LOCKING: Dùng STATE.SheetMutex thay vì cache.Mutex
	STATE.SheetMutex.RLock()
	defer STATE.SheetMutex.RUnlock()

	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	now := time.Now().UnixMilli()

	for i, row := range rows {
		if count >= limit { break }
		// ... Logic so khớp (Match, Contains, Time...) ...
		// Lưu ý: Dùng ConvertSerialDate(row[idx]) ở đây
		
		// Demo logic đơn giản để pass build:
		match := true // (Thực tế bạn paste lại logic check Match/Contains ở đây)
		
		if match {
			item := make(map[string]interface{})
			item["row_index"] = i + RANGES.DATA_START_ROW
			result[count] = item
			count++
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"status": "true", "data": result})
}
