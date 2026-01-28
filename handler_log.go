package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func HandleLogData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	token, _ := body["token"].(string)
	auth := CheckToken(token)
	if !auth.IsValid { return }

	dataList, _ := body["data"].([]interface{})
	if len(dataList) == 0 { return }

	rowsToAdd := make([][]interface{}, 0)
	sheetName := SHEET_NAMES.POST_LOGGER // Default

	for _, item := range dataList {
		obj, ok := item.(map[string]interface{})
		if !ok { continue }
		
		if s, ok := obj["sheet"].(string); ok { sheetName = s }
		
		// Convert obj "col_0" -> array index 0
		maxCol := 0
		for k := range obj {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(k[4:])
				if idx > maxCol { maxCol = idx }
			}
		}
		
		row := make([]interface{}, maxCol+1)
		for k, v := range obj {
			if strings.HasPrefix(k, "col_") {
				idx, _ := strconv.Atoi(k[4:])
				row[idx] = v
			}
		}
		rowsToAdd = append(rowsToAdd, row)
	}

	if len(rowsToAdd) > 0 {
		QueueAppend(auth.SpreadsheetID, sheetName, rowsToAdd)
	}
	
	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Đang xử lý ghi dữ liệu"})
}
