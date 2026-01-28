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

	// Smart Load (Dữ liệu đã phân vùng)
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}
	rows := cacheData.RawValues

	targetIndex := -1
	isAppend := false
	
	// 1. Parse row_index THÔNG MINH (Chấp nhận cả String và Int/Float)
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		switch val := v.(type) {
		case float64:
			rowIndexInput = int(val)
		case string:
			if val != "" {
				if i, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
					rowIndexInput = i
				}
			}
		case int:
			rowIndexInput = val
		}
	}

	// 2. Logic tìm dòng cần sửa
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

	// Trường hợp 1: Có row_index -> Truy cập trực tiếp (O(1))
	if hasRowIndex {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < len(rows) {
			if hasSearchCols {
				// Double check nếu client kỹ tính
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
		// Trường hợp 2: Search động (Phải quét mảng O(N))
		// (Update by search ít dùng nên O(N) là chấp nhận được)
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
		// Trường hợp 3: Append (Thêm mới)
		isAppend = true
	}

	// 3. Chuẩn bị dữ liệu ghi
	var newRow []interface{}
	oldNote := ""
	oldStatus := "" // Để track thay đổi status map

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
		newRow[INDEX_DATA_TIKTOK.NOTE] = makeNoteContent(oldNote, content, newStatus, mode)
	}

	// 4. Update RAM & Queue
	cacheKey := sid + KEY_SEPARATOR + sheetName
	
	if isAppend {
		// Append phức tạp hơn, tạm thời invalidate cache để load lại lần sau cho an toàn
		// Hoặc thêm vào cuối mảng RAM (nhưng cần handle StatusMap/AssignedMap)
		STATE.SheetMutex.Lock()
		delete(STATE.SheetCache, cacheKey) // Xóa cache để ép load lại
		STATE.SheetMutex.Unlock()
		
		QueueAppend(sid, sheetName, [][]interface{}{newRow})
		
		return &UpdateResponse{
			Status:          "true",
			Type:            "updated",
			Messenger:       "Thêm mới thành công",
			AuthProfile:     MakeAuthProfile(newRow),
			ActivityProfile: MakeActivityProfile(newRow),
			AiProfile:       MakeAiProfile(newRow),
		}, nil

	} else {
		// 🔥 UPDATE RAM & PARTITION MAPS (Logic quan trọng)
		STATE.SheetMutex.Lock()
		
		// A. Update Values
		cacheData.RawValues[targetIndex] = newRow
		
		// B. Update CleanValues
		for i := 0; i < CACHE.CLEAN_COL_LIMIT; i++ {
			if i < len(newRow) {
				cacheData.CleanValues[targetIndex][i] = CleanString(newRow[i])
			}
		}
		cacheData.LastAccessed = time.Now().UnixMilli()

		// C. 🔥 Update StatusMap (Di chuyển index nếu đổi trạng thái)
		if isDataTiktok {
			newCleanStatus := CleanString(newRow[INDEX_DATA_TIKTOK.STATUS])
			
			if newCleanStatus != oldStatus {
				// Xóa khỏi nhóm cũ
				if list, ok := cacheData.StatusMap[oldStatus]; ok {
					for k, v := range list {
						if v == targetIndex {
							// Xóa phần tử k (swap last or slice)
							cacheData.StatusMap[oldStatus] = append(list[:k], list[k+1:]...)
							break
						}
					}
				}
				// Thêm vào nhóm mới
				cacheData.StatusMap[newCleanStatus] = append(cacheData.StatusMap[newCleanStatus], targetIndex)
			}

			// D. Update AssignedMap (Nếu gán device mới - ít gặp ở luồng update nhưng vẫn nên làm)
			if deviceId != "" {
				cacheData.AssignedMap[deviceId] = targetIndex
			}
		}
		STATE.SheetMutex.Unlock()

		// Gửi xuống hàng đợi ghi đĩa
		QueueUpdate(sid, sheetName, targetIndex, newRow)
		
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

// Logic tạo Note (Giữ nguyên)
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
