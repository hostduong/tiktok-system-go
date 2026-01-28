package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func HandleLogData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi Body JSON"}`, 400)
		return
	}

	// Lấy Context
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		http.Error(w, `{"status":"false","messenger":"Lỗi xác thực"}`, 401)
		return
	}

	dataList, _ := body["data"].([]interface{})
	if len(dataList) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Không có dữ liệu để ghi"})
		return
	}

	rowsBySheet := make(map[string][][]interface{})

	// Gom nhóm data theo Sheet Name
	for _, item := range dataList {
		obj, ok := item.(map[string]interface{})
		if !ok { continue }
		
		sheetName := SHEET_NAMES.POST_LOGGER // Default
		if s, ok := obj["sheet"].(string); ok && s != "" {
			sheetName = s
		}
		
		// Tìm max col index
		maxCol := 0
		for k := range obj {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(k[4:])
				if idx > maxCol { maxCol = idx }
			}
		}
		
		// Tạo row
		row := make([]interface{}, maxCol+1)
		for i := range row { row[i] = "" } // Init empty string

		for k, v := range obj {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(k[4:])
				row[idx] = v
			}
		}
		
		rowsBySheet[sheetName] = append(rowsBySheet[sheetName], row)
	}

	// Đẩy vào Queue Append
	for sheet, rows := range rowsBySheet {
		if len(rows) > 0 {
			QueueAppend(tokenData.SpreadsheetID, sheet, rows)
		}
	}
	
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Đang xử lý ghi dữ liệu"})
}
