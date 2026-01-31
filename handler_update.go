package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

/*
=================================================================================================
📘 TÀI LIỆU API: CẬP NHẬT DỮ LIỆU (POST /tool/updated)
=================================================================================================

1. MỤC ĐÍCH:
   - Cập nhật thông tin tài khoản (Trạng thái, Ghi chú, Cookie...) vào hệ thống.
   - Hỗ trợ cập nhật 1 dòng hoặc nhiều dòng cùng lúc.
   - Tự động đồng bộ RAM để các tiến trình khác nhận diện thay đổi ngay lập tức.
   - 🔥 ĐẶC BIỆT: Khi cập nhật Note, hệ thống sẽ BẢO TOÀN số lần chạy cũ.

2. CẤU TRÚC BODY REQUEST:
{
  "type": "updated",          // Lệnh: "updated" (1 dòng) hoặc "updated_all" (nhiều dòng)
  "token": "...",             // Token xác thực
  "deviceId": "...",          // ID thiết bị (để map dữ liệu nếu cần)
  "sheet": "DataTiktok",      // (Tùy chọn) Tên sheet, mặc định là DataTiktok
  
  // --- PHẦN 1: ĐIỀU KIỆN TÌM KIẾM (FILTER) ---
  "row_index": 123,           // (Ưu tiên 1) Cập nhật chính xác dòng 123 (Index tính từ 0)
  
  "search_and": {             // (Ưu tiên 2) Tìm dòng thỏa mãn TẤT CẢ điều kiện
      "match_col_0": ["đang chạy"],
      "contains_col_6": ["@gmail.com"]
  },
  
  // --- PHẦN 2: DỮ LIỆU CẦN SỬA (UPDATED BLOCK) ---
  // QUY TẮC: Chỉ sử dụng key dạng "col_X" (X là số thứ tự cột, bắt đầu từ 0)
  "updated": {
      "col_0": "Đang chạy",              // Cập nhật Cột 0 (Status)
      "col_1": "Nội dung ghi chú mới",   // Cập nhật Cột 1 (Note) - Sẽ tự động giữ số lần chạy cũ
      "col_17": "cookie_mới_ở_đây"       // Cập nhật Cột 17 (Cookie)
  }
}
*/

type UpdateResponse struct {
	Status          string          `json:"status"`
	Type            string          `json:"type"`
	Messenger       string          `json:"messenger"`
	RowIndex        int             `json:"row_index,omitempty"`
	UpdatedCount    int             `json:"updated_count,omitempty"`
	AuthProfile     AuthProfile     `json:"auth_profile,omitempty"`
	ActivityProfile ActivityProfile `json:"activity_profile,omitempty"`
	AiProfile       AiProfile       `json:"ai_profile,omitempty"`
}

func HandleUpdateData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400); return
	}

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	if reqType == "" { reqType = "updated" }

	res, err := xu_ly_update_logic(sid, deviceId, reqType, body)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

func xu_ly_update_logic(sid, deviceId, reqType string, body map[string]interface{}) (*UpdateResponse, error) {
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	filters := parseFilterParams(body)
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok { if val, ok := toFloat(v); ok { rowIndexInput = int(val) } }

	updateData := prepareUpdateData(body)
	if len(updateData) == 0 { return nil, fmt.Errorf("Updated block trống") }

	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	updatedCount := 0
	lastUpdatedIdx := -1
	var lastUpdatedRow []interface{}

	// 1. UPDATE THEO ROW INDEX
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < len(rows) {
			if filters.HasFilter {
				if !isRowMatched(cleanRows[idx], rows[idx], filters) { return nil, fmt.Errorf("Row không khớp Filter") }
			}
			applyUpdateToRow(cacheData, idx, updateData, deviceId, isDataTiktok)
			QueueUpdate(sid, sheetName, idx, cacheData.RawValues[idx])
			
			return &UpdateResponse{
				Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
				RowIndex: rowIndexInput,
				AuthProfile: MakeAuthProfile(cacheData.RawValues[idx]),
				ActivityProfile: MakeActivityProfile(cacheData.RawValues[idx]),
				AiProfile: MakeAiProfile(cacheData.RawValues[idx]),
			}, nil
		} else { return nil, fmt.Errorf("Row không tồn tại") }
	}

	// 2. UPDATE THEO SEARCH
	if !filters.HasFilter { return nil, fmt.Errorf("Thiếu điều kiện tìm kiếm") }

	for i, cleanRow := range cleanRows {
		if isRowMatched(cleanRow, rows[i], filters) {
			applyUpdateToRow(cacheData, i, updateData, deviceId, isDataTiktok)
			QueueUpdate(sid, sheetName, i, cacheData.RawValues[i])
			updatedCount++
			lastUpdatedIdx = i
			lastUpdatedRow = cacheData.RawValues[i]
			if reqType == "updated" { break }
		}
	}

	if updatedCount == 0 { return nil, fmt.Errorf("Không tìm thấy dữ liệu") }

	if reqType == "updated_all" {
		return &UpdateResponse{
			Status: "true", Type: "updated_all",
			Messenger: fmt.Sprintf("Đã cập nhật %d tài khoản", updatedCount), UpdatedCount: updatedCount,
		}, nil
	}

	return &UpdateResponse{
		Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
		RowIndex: RANGES.DATA_START_ROW + lastUpdatedIdx,
		AuthProfile: MakeAuthProfile(lastUpdatedRow), ActivityProfile: MakeActivityProfile(lastUpdatedRow), AiProfile: MakeAiProfile(lastUpdatedRow),
	}, nil
}

func prepareUpdateData(body map[string]interface{}) map[int]interface{} {
	cols := make(map[int]interface{})
	if v, ok := body["updated"]; ok {
		if updatedMap, ok := v.(map[string]interface{}); ok {
			for k, val := range updatedMap {
				if strings.HasPrefix(k, "col_") {
					if idxStr := strings.TrimPrefix(k, "col_"); idxStr != "" {
						if idx, err := strconv.Atoi(idxStr); err == nil { cols[idx] = val }
					}
				}
			}
		}
	}
	return cols
}

func applyUpdateToRow(cache *SheetCacheData, idx int, updateCols map[int]interface{}, deviceId string, isDataTiktok bool) {
	row := cache.RawValues[idx]
	cleanRow := cache.CleanValues[idx]
	oldStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
	oldDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]

	// 🔥 LẤY NOTE CŨ RA TRƯỚC KHI VÒNG LẶP UPDATE CHẠY
	// (Fix lỗi: Nếu chạy vòng lặp trước, note cũ sẽ bị đè mất, làm hàm tạo note sau đó reset về 1)
	realOldNote := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.NOTE])

	// 1. Apply Data
	for colIdx, val := range updateCols {
		if colIdx >= 0 && colIdx < len(row) {
			row[colIdx] = val
			if colIdx < CACHE.CLEAN_COL_LIMIT { cleanRow[colIdx] = CleanString(val) }
		}
	}

	// 2. Logic DataTiktok (Sync Map & Note)
	if isDataTiktok {
		if deviceId != "" {
			row[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
			cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(deviceId)
		}

		_, hasSt := updateCols[INDEX_DATA_TIKTOK.STATUS]
		_, hasNote := updateCols[INDEX_DATA_TIKTOK.NOTE]
		if hasSt || hasNote {
			content := ""; if v, ok := updateCols[INDEX_DATA_TIKTOK.NOTE]; ok { content = fmt.Sprintf("%v", v) }
			
			// Dùng realOldNote (đã capture ở trên) để đảm bảo giữ nguyên số lần chạy
			newStatus := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.STATUS])
			finalNote := tao_ghi_chu_chuan_update(realOldNote, content, newStatus)
			
			row[INDEX_DATA_TIKTOK.NOTE] = finalNote
			cleanRow[INDEX_DATA_TIKTOK.NOTE] = CleanString(finalNote)
		}

		// Sync RAM
		newStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
		if newStatus != oldStatus {
			removeFromStatusMap(cache.StatusMap, oldStatus, idx)
			cache.StatusMap[newStatus] = append(cache.StatusMap[newStatus], idx)
		}
		newDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
		if newDev != oldDev {
			if oldDev != "" { delete(cache.AssignedMap, oldDev) } else { removeFromIntList(&cache.UnassignedList, idx) }
			if newDev != "" { cache.AssignedMap[newDev] = idx } else { cache.UnassignedList = append(cache.UnassignedList, idx) }
		}
	}
	cache.LastAccessed = time.Now().UnixMilli()
}

// Logic tạo Note UPDATE: GIỮ NGUYÊN số lần chạy
func tao_ghi_chu_chuan_update(oldNote, content, newStatus string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	oldNote = SafeString(oldNote)
	count := 1
	// Bắt số lần từ note cũ
	match := REGEX_COUNT.FindStringSubmatch(oldNote)
	if len(match) > 1 { if c, err := strconv.Atoi(match[1]); err == nil { count = c } }

	statusToUse := content
	if statusToUse == "" { statusToUse = newStatus }
	if statusToUse == "" {
		lines := strings.Split(oldNote, "\n")
		if len(lines) > 0 { statusToUse = lines[0] }
	}
	if statusToUse == "" { statusToUse = "Đang chạy" }

	return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
}
