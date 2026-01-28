package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type UpdateResponse struct {
	Status          string          `json:"status"`
	Type            string          `json:"type"`
	Messenger       string          `json:"messenger"`
	RowIndex        int             `json:"row_index,omitempty"`
	AuthProfile     AuthProfile     `json:"auth_profile"`     // 🔥 Dùng Struct
	ActivityProfile ActivityProfile `json:"activity_profile"` // 🔥 Dùng Struct
	AiProfile       AiProfile       `json:"ai_profile"`       // 🔥 Dùng Struct
}

func HandleUpdateData(w http.ResponseWriter, r *http.Request) {
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

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])

	res, err := xu_ly_cap_nhat_du_lieu(sid, deviceId, body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func xu_ly_cap_nhat_du_lieu(sid, deviceId string, body map[string]interface{}) (*UpdateResponse, error) {
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }
	rows := cacheData.RawValues

	targetIndex := -1
	isAppend := false
	rowIndexInput := -1
	if v, ok := body["row_index"].(float64); ok { rowIndexInput = int(v) }

	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < len(rows) { targetIndex = idx }
	} else { isAppend = true }

	var newRow []interface{}
	
	if isAppend {
		newRow = make([]interface{}, 61)
		for i := range newRow { newRow[i] = "" }
	} else {
		srcRow := rows[targetIndex]
		newRow = make([]interface{}, 61)
		for i := 0; i < 61; i++ {
			if i < len(srcRow) { newRow[i] = srcRow[i] } else { newRow[i] = "" }
		}
	}

	for k, v := range body {
		if strings.HasPrefix(k, "col_") {
			idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
			if idx < 61 { newRow[idx] = v }
		}
	}
	
	if isDataTiktok {
		if deviceId != "" { newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId }
		content := CleanString(body["note"])
		now := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
		st := CleanString(newRow[INDEX_DATA_TIKTOK.STATUS])
		if st == "" { st = "Đang chờ" }
		if content != "" {
			newRow[INDEX_DATA_TIKTOK.NOTE] = fmt.Sprintf("%s\n%s", st, now)
		}
	}

	if isAppend {
		STATE.SheetMutex.Lock()
		for k := range STATE.SheetCache {
			if strings.HasPrefix(k, sid+KEY_SEPARATOR) { delete(STATE.SheetCache, k) }
		}
		STATE.SheetMutex.Unlock()
		QueueAppend(sid, sheetName, [][]interface{}{newRow})
		
		return &UpdateResponse{
			Status: "true", Type: "updated", Messenger: "Thêm mới thành công",
			AuthProfile:     MakeAuthProfile(newRow),
			ActivityProfile: MakeActivityProfile(newRow),
			AiProfile:       MakeAiProfile(newRow),
		}, nil
	} else {
		QueueUpdate(sid, sheetName, targetIndex, newRow)
		return &UpdateResponse{
			Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
			RowIndex: RANGES.DATA_START_ROW + targetIndex,
			AuthProfile:     MakeAuthProfile(newRow),
			ActivityProfile: MakeActivityProfile(newRow),
			AiProfile:       MakeAiProfile(newRow),
		}, nil
	}
}
