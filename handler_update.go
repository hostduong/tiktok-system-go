package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// =================================================================================================
// 🔥 CẤU TRÚC PHẢN HỒI CHUẨN CHO UPDATE (Khớp Node.js Source 313, 317)
// =================================================================================================

type UpdateResponse struct {
	Status          string            `json:"status"`
	Type            string            `json:"type"`
	Messenger       string            `json:"messenger"`
	RowIndex        int               `json:"row_index,omitempty"`
	AuthProfile     map[string]string `json:"auth_profile"`
	ActivityProfile map[string]string `json:"activity_profile"`
	AiProfile       map[string]string `json:"ai_profile"`
}

// =================================================================================================
// 🟢 MAIN HANDLER
// =================================================================================================

func HandleUpdate(w http.ResponseWriter, r *http.Request) {
	// 1. Parse Body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi Body JSON"}`, 400)
		return
	}

	// 2. Lấy thông tin từ Context
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		http.Error(w, `{"status":"false","messenger":"Lỗi xác thực"}`, 401)
		return
	}

	sid := tokenData.SpreadsheetId
	// DeviceId có thể null trong luồng update, lấy từ body nếu có
	deviceId := CleanString(body["deviceId"])

	// 3. Xử lý Logic
	res, err := xu_ly_cap_nhat_du_lieu(sid, deviceId, body)
	if err != nil {
		// Trả về lỗi chuẩn JSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}

	// 4. Trả về kết quả JSON đẹp
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC NGHIỆP VỤ (Port từ Node.js Source 289 - xu_ly_cap_nhat_du_lieu)
// =================================================================================================

func xu_ly_cap_nhat_du_lieu(sid, deviceId string, body map[string]interface{}) (*UpdateResponse, error) {
	sheetName := CleanString(body["sheet"])
	if sheetName == "" {
		sheetName = SHEET_NAMES.DATA_TIKTOK
	}
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	// 1. Tải dữ liệu
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}
	rows := cacheData.RawValues

	targetIndex := -1
	isAppend := false
	
	// Parse row_index từ body
	rowIndexInput := -1
	if v, ok := body["row_index"].(float64); ok {
		rowIndexInput = int(v)
	}

	// 2. Phân loại cột Search và Update
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

	// 3. Xác định Target Index
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
		// Tìm kiếm tuần tự (nếu không có row_index)
		for i, cleanRow := range cacheData.CleanValues {
			match := true
			for colIdx, val := range searchCols {
				cellVal := ""
				if colIdx < len(cleanRow) {
					cellVal = cleanRow[colIdx]
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

	// 4. Chuẩn bị dữ liệu Ghi
	var newRow []interface{}
	oldNote := ""

	if isAppend {
		newRow = make([]interface{}, 61)
		for i := range newRow { newRow[i] = "" } // Init empty
	} else {
		if isDataTiktok {
			oldNote = CleanString(rows[targetIndex][INDEX_DATA_TIKTOK.NOTE])
		}
		// Clone row cũ
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

	// 5. Áp dụng Update
	for idx, val := range updateCols {
		if idx < 61 {
			newRow[idx] = val
		}
	}
	if deviceId != "" && isDataTiktok {
		newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	}

	// 6. Xử lý Note chuẩn (Source 308)
	if isDataTiktok {
		content := CleanString(body["note"])
		if content == "" {
			if v, ok := updateCols[INDEX_DATA_TIKTOK.NOTE]; ok {
				content = CleanString(v)
			}
		}
		
		// Logic tạo Note (Source 48-56 V243)
		now := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
		newStatus := CleanString(newRow[INDEX_DATA_TIKTOK.STATUS])
		
		finalNote := ""
		if isAppend {
			if newStatus == "" { newStatus = "Đang chờ" }
			finalNote = fmt.Sprintf("%s\n%s", newStatus, now)
		} else {
			// Update mode
			// Simplified regex logic for Go: Just append time
			finalNote = fmt.Sprintf("%s\n%s", newStatus, now)
			// (Bạn có thể thêm logic đếm lần ở đây nếu cần thiết, hiện tại để đơn giản giống form Login)
		}
		
		newRow[INDEX_DATA_TIKTOK.NOTE] = finalNote
	}

	// 7. Ghi vào Sheet & Cache
	if isAppend {
		// Append thì clear cache để load lại sau (Source 311)
		STATE.SheetMutex.Lock()
		for k := range STATE.SheetCache {
			if strings.HasPrefix(k, sid+KEY_SEPARATOR) {
				delete(STATE.SheetCache, k)
			}
		}
		STATE.SheetMutex.Unlock()
		
		QueueAppend(sid, sheetName, [][]interface{}{newRow})
		
		return &UpdateResponse{
			Status:          "true",
			Type:            "updated",
			Messenger:       "Thêm mới thành công",
			AuthProfile:     mapProfileSafe(newRow, 0, 22),
			ActivityProfile: mapProfileSafe(newRow, 23, 44),
			AiProfile:       mapProfileSafe(newRow, 45, 60),
		}, nil

	} else {
		// Update (Source 316)
		QueueUpdate(sid, sheetName, targetIndex, newRow)
		
		return &UpdateResponse{
			Status:          "true",
			Type:            "updated",
			Messenger:       "Cập nhật thành công",
			RowIndex:        RANGES.DATA_START_ROW + targetIndex,
			AuthProfile:     mapProfileSafe(newRow, 0, 22),
			ActivityProfile: mapProfileSafe(newRow, 23, 44),
			AiProfile:       mapProfileSafe(newRow, 45, 60),
		}, nil
	}
}

// =================================================================================================
// 🟢 HELPER FUNCTIONS (LOCAL)
// =================================================================================================

// mapProfileSafe: Map dữ liệu sang JSON Profile với tên cột chữ thường & Value là String an toàn
func mapProfileSafe(row []interface{}, start, end int) map[string]string {
	res := make(map[string]string)
	for i := start; i <= end; i++ {
		// Tìm tên key từ Map Index global (được init bên handler_login.go)
		// Hoặc fallback nếu chưa init (Dự phòng)
		keyName := ""
		if INDEX_TO_KEY != nil {
			keyName = INDEX_TO_KEY[i]
		}
		
		if keyName != "" {
			if i < len(row) {
				res[keyName] = SafeStringUpdate(row[i]) // Ép kiểu về String
			} else {
				res[keyName] = ""
			}
		}
	}
	return res
}

// SafeStringUpdate: Xử lý số to (Password) thành chuỗi không bị e+08
func SafeStringUpdate(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		// Nếu là số nguyên, in không thập phân
		if val == float64(int64(val)) {
			return fmt.Sprintf("%.0f", val)
		}
		return fmt.Sprintf("%v", val)
	case int:
		return fmt.Sprintf("%d", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}
