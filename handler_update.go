package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// [CẬP NHẬT] Sử dụng map[string]interface{} để tương thích với utils.go mới
type UpdateResponse struct {
	Status          string                 `json:"status"`
	Type            string                 `json:"type"`
	Messenger       string                 `json:"messenger"`
	RowIndex        int                    `json:"row_index,omitempty"`
	AuthProfile     map[string]interface{} `json:"auth_profile"`
	ActivityProfile map[string]interface{} `json:"activity_profile"`
	AiProfile       map[string]interface{} `json:"ai_profile"`
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
	
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

func xu_ly_cap_nhat_du_lieu(sid, deviceId string, body map[string]interface{}) (*UpdateResponse, error) {
	sheetName := CleanString(body["sheet"])
	if sheetName == "" {
		sheetName = SHEET_NAMES.DATA_TIKTOK
	}
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	// Smart Load
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}
	rows := cacheData.RawValues

	targetIndex := -1
	isAppend := false
	
	// 1. Parse row_index (Sử dụng hàm toFloat từ utils.go)
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			rowIndexInput = int(val)
		}
	}

	// 2. Logic tìm dòng cần sửa
	searchCols := make(map[int]string)
	updateCols := make(map[int]interface{})

	for k, v := range body {
		if strings.HasPrefix(k, "search_col_") {
			if idxStr := strings.TrimPrefix(k, "search_col_"); idxStr != "" {
				if idx, err := strconv.Atoi(idxStr); err == nil {
					searchCols[idx] = CleanString(v)
				}
			}
		} else if strings.HasPrefix(k, "col_") {
			if idxStr := strings.TrimPrefix(k, "col_"); idxStr != "" {
				if idx, err := strconv.Atoi(idxStr); err == nil {
					updateCols[idx] = v
				}
			}
		}
	}

	hasRowIndex := (rowIndexInput >= RANGES.DATA_START_ROW)
	hasSearchCols := (len(searchCols) > 0)

	// A. Truy cập trực tiếp (O(1))
	if hasRowIndex {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < len(rows) {
			if hasSearchCols {
				match := true
				for colIdx, val := range searchCols {
					cellVal := ""
					if colIdx < len(cacheData.CleanValues[idx]) {
						cellVal = cacheData.CleanValues[idx][colIdx]
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
		// B. Search động (O(N))
		for i, row := range cacheData.CleanValues {
			match := true
			for colIdx, val := range searchCols {
				cellVal := ""
				if colIdx < len(row) {
					cellVal = row[colIdx]
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
			return nil, fmt.Errorf("Không tìm thấy dữ liệu phù hợp")
		}
	} else {
		// C. Thêm mới (Append)
		isAppend = true
	}

	// 3. Chuẩn bị dữ liệu ghi
	var newRow []interface{}
	oldNote := ""
	oldStatus := ""

	if isAppend {
		newRow = make([]interface{}, 61)
		for i := range newRow { newRow[i] = "" }
	} else {
		if isDataTiktok {
			oldNote = fmt.Sprintf("%v", rows[targetIndex][INDEX_DATA_TIKTOK.NOTE])
			if INDEX_DATA_TIKTOK.STATUS < len(cacheData.CleanValues[targetIndex]) {
				oldStatus = cacheData.CleanValues[targetIndex][INDEX_DATA_TIKTOK.STATUS]
			}
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
		
		// Sử dụng hàm tạo note từ handler_login (hoặc copy sang utils nếu cần dùng chung)
		// Ở đây ta gọi hàm local hoặc từ utils. 
		// Để an toàn, ta dùng logic tạo note trực tiếp ở đây hoặc giả định hàm tao_ghi_chu_chuan đã được move sang utils.go
		// TUY NHIÊN: Để tránh lỗi undefined, ta dùng hàm tao_ghi_chu_chuan_update nội bộ.
		newRow[INDEX_DATA_TIKTOK.NOTE] = tao_ghi_chu_chuan_update(oldNote, content, newStatus, mode)
	}

	// 4. Update RAM & Queue
	cacheKey := sid + KEY_SEPARATOR + sheetName
	
	if isAppend {
		STATE.SheetMutex.Lock()
		delete(STATE.SheetCache, cacheKey) // Invalidate cache để load lại
		STATE.SheetMutex.Unlock()
		
		QueueAppend(sid, sheetName, [][]interface{}{newRow})
		
		// [SỬA LỖI] Dùng AnhXa... thay vì Make...
		return &UpdateResponse{
			Status:          "true",
			Type:            "updated",
			Messenger:       "Thêm mới thành công",
			AuthProfile:     AnhXaAuth(newRow),
			ActivityProfile: AnhXaActivity(newRow),
			AiProfile:       AnhXaAi(newRow),
		}, nil

	} else {
		STATE.SheetMutex.Lock()
		
		cacheData.RawValues[targetIndex] = newRow
		
		for i := 0; i < CACHE.CLEAN_COL_LIMIT; i++ {
			if i < len(newRow) {
				cacheData.CleanValues[targetIndex][i] = CleanString(newRow[i])
			}
		}
		cacheData.LastAccessed = time.Now().UnixMilli()

		if isDataTiktok {
			newCleanStatus := CleanString(newRow[INDEX_DATA_TIKTOK.STATUS])
			if newCleanStatus != oldStatus {
				if list, ok := cacheData.StatusMap[oldStatus]; ok {
					for k, v := range list {
						if v == targetIndex {
							cacheData.StatusMap[oldStatus] = append(list[:k], list[k+1:]...)
							break
						}
					}
				}
				cacheData.StatusMap[newCleanStatus] = append(cacheData.StatusMap[newCleanStatus], targetIndex)
			}
			if deviceId != "" {
				cacheData.AssignedMap[deviceId] = targetIndex
			}
		}
		STATE.SheetMutex.Unlock()

		QueueUpdate(sid, sheetName, targetIndex, newRow)
		
		// [SỬA LỖI] Dùng AnhXa... thay vì Make...
		return &UpdateResponse{
			Status:          "true",
			Type:            "updated",
			Messenger:       "Cập nhật thành công",
			RowIndex:        RANGES.DATA_START_ROW + targetIndex,
			AuthProfile:     AnhXaAuth(newRow),
			ActivityProfile: AnhXaActivity(newRow),
			AiProfile:       AnhXaAi(newRow),
		}, nil
	}
}

// Logic tạo Note (Local version để tránh phụ thuộc chéo)
func tao_ghi_chu_chuan_update(oldNote, content, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" {
		st := newStatus
		if st == "" { st = "Đang chờ" }
		return st + "\n" + nowFull
	}
	count := 0
	oldNote = strings.TrimSpace(oldNote)
	lines := strings.Split(oldNote, "\n")
	
	if idx := strings.Index(oldNote, "(Lần"); idx != -1 {
		end := strings.Index(oldNote[idx:], ")")
		if end != -1 {
			if c, err := strconv.Atoi(strings.TrimSpace(oldNote[idx+len("(Lần") : idx+end])); err == nil {
				count = c
			}
		}
	}
	if count == 0 { count = 1 }

	statusToUse := content
	if statusToUse == "" { statusToUse = newStatus }
	if statusToUse == "" && len(lines) > 0 { statusToUse = lines[0] }
	if statusToUse == "" { statusToUse = "Đang chạy" }
	
	return statusToUse + "\n" + nowFull + " (Lần " + strconv.Itoa(count) + ")"
}
