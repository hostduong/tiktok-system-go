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
	AuthProfile     AuthProfile     `json:"auth_profile"`
	ActivityProfile ActivityProfile `json:"activity_profile"`
	AiProfile       AiProfile       `json:"ai_profile"`
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
	if sheetName == "" {
		sheetName = SHEET_NAMES.DATA_TIKTOK
	}
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}
	rows := cacheData.RawValues

	targetIndex := -1
	isAppend := false
	rowIndexInput := -1
	if v, ok := body["row_index"].(float64); ok {
		rowIndexInput = int(v)
	}

	searchCols := make(map[int]string)
	updateCols := make(map[int]interface{})

	for k, v := range body {
		if strings.HasPrefix(k, "search_col_") {
			idx, _ := strconv.Atoi(strings.TrimPrefix(k, "search_col_"))
			searchCols[idx] = CleanString(v)
		} else if strings.HasPrefix(k, "col_") {
			idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
			updateCols[idx] = v
		}
	}

	hasRowIndex := (rowIndexInput >= RANGES.DATA_START_ROW)
	hasSearchCols := (len(searchCols) > 0)

	if hasRowIndex {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < len(rows) {
			if hasSearchCols {
				match := true
				for colIdx, val := range searchCols {
					cellVal := ""
					if colIdx < CACHE.CLEAN_COL_LIMIT {
						cellVal = cacheData.CleanValues[idx][colIdx]
					} else {
						cellVal = CleanString(rows[idx][colIdx])
					}
					if cellVal != val {
						match = false
						break
					}
				}
				if !match {
					return nil, fmt.Errorf("Dữ liệu không khớp")
				}
			}
			targetIndex = idx
		} else {
			return nil, fmt.Errorf("Dòng yêu cầu không tồn tại")
		}
	} else if hasSearchCols {
		for i, row := range rows {
			match := true
			for colIdx, val := range searchCols {
				cellVal := ""
				if colIdx < CACHE.CLEAN_COL_LIMIT {
					cellVal = cacheData.CleanValues[i][colIdx]
				} else {
					cellVal = CleanString(row[colIdx])
				}
				if cellVal != val {
					match = false
					break
				}
			}
			if match {
				targetIndex = i
				break
			}
		}
		if targetIndex == -1 {
			return nil, fmt.Errorf("Không tìm thấy nick phù hợp")
		}
	} else {
		isAppend = true
	}

	var newRow []interface{}
	oldNote := ""

	if isAppend {
		newRow = make([]interface{}, 61)
		for i := range newRow { newRow[i] = "" }
	} else {
		if isDataTiktok {
			oldNote = fmt.Sprintf("%v", rows[targetIndex][INDEX_DATA_TIKTOK.NOTE])
		}
		srcRow := rows[targetIndex]
		newRow = make([]interface{}, 61)
		for i := 0; i < 61; i++ {
			if i < len(srcRow) {
				newRow[i] = srcRow[i]
			} else {
				newRow[i] = ""
			}
		}
	}

	for idx, val := range updateCols {
		if idx < 61 {
			newRow[idx] = val
		}
	}
	
	if deviceId != "" && isDataTiktok {
		newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	}

	if isDataTiktok {
		content := ""
		if v, ok := body["note"].(string); ok {
			content = v
		} else if v, ok := updateCols[INDEX_DATA_TIKTOK.NOTE]; ok {
			content = fmt.Sprintf("%v", v)
		}
		mode := "updated"
		if isAppend { mode = "new" }
		newStatus := fmt.Sprintf("%v", newRow[INDEX_DATA_TIKTOK.STATUS])
		newRow[INDEX_DATA_TIKTOK.NOTE] = makeNoteContent(oldNote, content, newStatus, mode)
	}

	cacheKey := sid + KEY_SEPARATOR + sheetName
	
	if isAppend {
		STATE.SheetMutex.Lock()
		delete(STATE.SheetCache, cacheKey)
		STATE.SheetMutex.Unlock()
		
		GoogleServiceAppend(sid, sheetName, [][]interface{}{newRow})
		
		return &UpdateResponse{
			Status:          "true",
			Type:            "updated",
			Messenger:       "Thêm mới thành công",
			AuthProfile:     MakeAuthProfile(newRow),
			ActivityProfile: MakeActivityProfile(newRow),
			AiProfile:       MakeAiProfile(newRow),
		}, nil

	} else {
		STATE.SheetMutex.Lock()
		if cache, ok := STATE.SheetCache[cacheKey]; ok {
			cache.RawValues[targetIndex] = newRow
			cleanR := make([]string, CACHE.CLEAN_COL_LIMIT)
			for i := 0; i < CACHE.CLEAN_COL_LIMIT; i++ {
				cleanR[i] = CleanString(newRow[i])
			}
			cache.CleanValues[targetIndex] = cleanR
			cache.LastAccessed = time.Now().UnixMilli()
		}
		STATE.SheetMutex.Unlock()

		GoogleServiceUpdate(sid, sheetName, targetIndex, newRow)
		
		return &UpdateResponse{
			Status:          "true",
			Type:            "updated",
			Messenger:       "Cập nhật thành công",
			RowIndex:        RANGES.DATA_START_ROW + targetIndex,
			AuthProfile:     MakeAuthProfile(newRow),
			ActivityProfile: MakeActivityProfile(newRow),
			AiProfile:       MakeAiProfile(newRow),
		}, nil
	}
}

// 🔥 FIX: Thay đổi kiểu dữ liệu thành string cho sid và sheet
func GoogleServiceUpdate(sid string, sheet string, rowIdx int, data []interface{}) {
	QueueUpdate(sid, sheet, rowIdx, data)
}
func GoogleServiceAppend(sid string, sheet string, data [][]interface{}) {
	QueueAppend(sid, sheet, data)
}

func makeNoteContent(oldNote, content, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" {
		st := newStatus
		if st == "" { st = "Đang chờ" }
		return fmt.Sprintf("%s\n%s", st, nowFull)
	}
	count := 0
	oldNote = strings.TrimSpace(oldNote)
	lines := strings.Split(oldNote, "\n")
	lastLine := ""
	if len(lines) > 0 { lastLine = lines[len(lines)-1] }
	if idx := strings.Index(lastLine, "(Lần"); idx != -1 {
		endIdx := strings.Index(lastLine[idx:], ")")
		if endIdx != -1 {
			numStr := lastLine[idx+len("(Lần") : idx+endIdx]
			c, _ := strconv.Atoi(strings.TrimSpace(numStr))
			count = c
		}
	}
	if count == 0 { count = 1 }
	statusToUse := content
	if statusToUse == "" { statusToUse = newStatus }
	if statusToUse == "" && len(lines) > 0 { statusToUse = lines[0] }
	if statusToUse == "" { statusToUse = "Đang chạy" }
	return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
}
