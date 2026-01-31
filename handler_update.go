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
📘 TÀI LIỆU API UPDATE (POST /tool/updated)
=================================================================================================
JSON Input Chuẩn:
{
  "type": "updated", // hoặc "updated_all"
  "token": "...",
  "deviceId": "...",
  "row_index": 123, // (Optional)
  "search_and": { ... },
  "search_or": { ... },
  "updated": {
      "col_10": "New Value",
      "status": "New Status",
      "note": "New Note"
  }
}
*/

// =================================================================================================
// 🟢 CẤU TRÚC RESPONSE
// =================================================================================================

type UpdateResponse struct {
	Status          string          `json:"status"`
	Type            string          `json:"type"`            // "updated" hoặc "updated_all"
	Messenger       string          `json:"messenger"`
	RowIndex        int             `json:"row_index,omitempty"` // Chỉ có khi updated đơn lẻ
	UpdatedCount    int             `json:"updated_count,omitempty"` // Chỉ có khi updated_all
	AuthProfile     AuthProfile     `json:"auth_profile,omitempty"`
	ActivityProfile ActivityProfile `json:"activity_profile,omitempty"`
	AiProfile       AiProfile       `json:"ai_profile,omitempty"`
}

// =================================================================================================
// 🟢 HANDLER CHÍNH
// =================================================================================================

func HandleUpdateData(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
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

// =================================================================================================
// 🟢 LOGIC LÕI
// =================================================================================================

func xu_ly_update_logic(sid, deviceId, reqType string, body map[string]interface{}) (*UpdateResponse, error) {
	// 1. Xác định Sheet
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	// 2. Tải dữ liệu
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	// 3. Parse Filter (Root)
	filters := parseFilterParams(body)
	
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok { rowIndexInput = int(val) }
	}

	// 4. Parse Data Update (Từ block "updated")
	updateData := prepareUpdateData(body)
	if len(updateData) == 0 {
		return nil, fmt.Errorf("Không có dữ liệu để cập nhật (block 'updated' trống)")
	}

	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues

	updatedCount := 0
	lastUpdatedIdx := -1
	var lastUpdatedRow []interface{}

	// --- CHIẾN LƯỢC 1: ROW INDEX ---
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < len(rows) {
			if filters.HasFilter {
				if !isRowMatched(cleanRows[idx], rows[idx], filters) {
					return nil, fmt.Errorf("Dữ liệu dòng %d không khớp điều kiện lọc", rowIndexInput)
				}
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
		} else {
			return nil, fmt.Errorf("Dòng yêu cầu không tồn tại")
		}
	}

	// --- CHIẾN LƯỢC 2: SEARCH FILTER ---
	if !filters.HasFilter {
		return nil, fmt.Errorf("Thiếu điều kiện tìm kiếm")
	}

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

	if updatedCount == 0 { return nil, fmt.Errorf("Không tìm thấy dữ liệu phù hợp") }

	if reqType == "updated_all" {
		return &UpdateResponse{
			Status: "true", Type: "updated_all",
			Messenger: fmt.Sprintf("Đã cập nhật thành công %d tài khoản", updatedCount),
			UpdatedCount: updatedCount,
		}, nil
	}

	return &UpdateResponse{
		Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
		RowIndex: RANGES.DATA_START_ROW + lastUpdatedIdx,
		AuthProfile: MakeAuthProfile(lastUpdatedRow),
		ActivityProfile: MakeActivityProfile(lastUpdatedRow),
		AiProfile: MakeAiProfile(lastUpdatedRow),
	}, nil
}

// =================================================================================================
// 🛠 HELPER FUNCTIONS
// =================================================================================================

// 🔥 Sửa lại: Chỉ lấy dữ liệu trong block "updated"
func prepareUpdateData(body map[string]interface{}) map[int]interface{} {
	cols := make(map[int]interface{})
	
	// Kiểm tra xem có key "updated" không
	if v, ok := body["updated"]; ok {
		if updatedMap, ok := v.(map[string]interface{}); ok {
			for k, val := range updatedMap {
				// 1. Quét col_X
				if strings.HasPrefix(k, "col_") {
					if idxStr := strings.TrimPrefix(k, "col_"); idxStr != "" {
						if idx, err := strconv.Atoi(idxStr); err == nil {
							cols[idx] = val
						}
					}
				}
				// 2. Quét các key đặc biệt (status, note)
				if k == "status" { cols[INDEX_DATA_TIKTOK.STATUS] = val }
				if k == "note" { cols[INDEX_DATA_TIKTOK.NOTE] = val }
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

	// 1. Apply Data
	for colIdx, val := range updateCols {
		if colIdx >= 0 && colIdx < len(row) {
			row[colIdx] = val
			if colIdx < CACHE.CLEAN_COL_LIMIT {
				cleanRow[colIdx] = CleanString(val)
			}
		}
	}

	// 2. Logic DataTiktok
	if isDataTiktok {
		if deviceId != "" {
			row[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
			cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(deviceId)
		}

		// Xử lý Note
		content := ""
		if v, ok := updateCols[INDEX_DATA_TIKTOK.NOTE]; ok { content = fmt.Sprintf("%v", v) }
		
		oldNoteInRow := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.NOTE]) 
		newStatusRaw := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.STATUS])
		
		_, hasSt := updateCols[INDEX_DATA_TIKTOK.STATUS]
		_, hasNote := updateCols[INDEX_DATA_TIKTOK.NOTE]
		
		if hasSt || hasNote {
			finalNote := tao_ghi_chu_chuan_update(oldNoteInRow, content, newStatusRaw)
			row[INDEX_DATA_TIKTOK.NOTE] = finalNote
			cleanRow[INDEX_DATA_TIKTOK.NOTE] = CleanString(finalNote)
		}

		// 3. Sync Maps
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

func removeFromIntList(list *[]int, target int) {
	for i, v := range *list {
		if v == target {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return
		}
	}
}

func tao_ghi_chu_chuan_update(oldNote, content, newStatus string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	count := 0
	oldNote = strings.TrimSpace(oldNote)
	lines := strings.Split(oldNote, "\n")
	if idx := strings.Index(oldNote, "(Lần"); idx != -1 {
		end := strings.Index(oldNote[idx:], ")")
		if end != -1 {
			if c, err := strconv.Atoi(strings.TrimSpace(oldNote[idx+len("(Lần") : idx+end])); err == nil { count = c }
		}
	}
	if count == 0 { count = 1 }
	statusToUse := content
	if statusToUse == "" { statusToUse = newStatus }
	if statusToUse == "" && len(lines) > 0 { statusToUse = lines[0] }
	if statusToUse == "" { statusToUse = "Đang chạy" }
	return statusToUse + "\n" + nowFull + " (Lần " + strconv.Itoa(count) + ")"
}
