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
Chức năng: Chỉ CẬP NHẬT thông tin (Không Thêm Mới).

1. Update Đơn lẻ (type="updated"):
   - Ưu tiên 1: Theo row_index.
   - Ưu tiên 2: Theo Filter (Tìm nick đầu tiên khớp -> Sửa -> Dừng).
   - Nếu không tìm thấy -> BÁO LỖI.

2. Update Hàng loạt (type="updated_all"):
   - Bắt buộc phải có Filter.
   - Quét toàn bộ danh sách.
   - Sửa TẤT CẢ các nick khớp điều kiện.
   - Trả về số lượng đã sửa (updated_count).
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
	
	// Mặc định là updated nếu không gửi
	if reqType == "" { reqType = "updated" }

	// Gọi hàm xử lý
	res, err := xu_ly_update_logic(sid, deviceId, reqType, body)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC LÕI: TÌM & SỬA (KHÔNG APPEND)
// =================================================================================================

func xu_ly_update_logic(sid, deviceId, reqType string, body map[string]interface{}) (*UpdateResponse, error) {
	// 1. Xác định Sheet
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	// 2. Tải dữ liệu
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	// 3. Parse Filter & Params
	filters := parseFilterParams(body) // Hàm từ utils.go
	
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok { rowIndexInput = int(val) }
	}

	// Xác định các cột cần update (col_X, status, note)
	updateData := prepareUpdateData(body)

	// 🔒 KHÓA GHI (Lock toàn bộ để đảm bảo an toàn)
	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues

	// Biến lưu kết quả
	updatedCount := 0
	lastUpdatedIdx := -1
	var lastUpdatedRow []interface{}

	// --- 📍 CHIẾN LƯỢC 1: UPDATE THEO ROW_INDEX (TUYỆT ĐỐI) ---
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < len(rows) {
			// Validation: Nếu có filter, phải khớp mới sửa
			if filters.HasFilter {
				if !isRowMatched(cleanRows[idx], rows[idx], filters) {
					return nil, fmt.Errorf("Dữ liệu dòng %d không khớp điều kiện", rowIndexInput)
				}
			}
			
			// Thực hiện Update
			applyUpdateToRow(cacheData, idx, updateData, deviceId, isDataTiktok)
			
			// Queue Update
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

	// --- 📍 CHIẾN LƯỢC 2: UPDATE THEO SEARCH ---
	
	// Bắt buộc phải có Filter mới được chạy chế độ này
	if !filters.HasFilter {
		return nil, fmt.Errorf("Thiếu điều kiện tìm kiếm (Không hỗ trợ Append)")
	}

	// Quét toàn bộ danh sách
	for i, cleanRow := range cleanRows {
		// Kiểm tra khớp Filter
		if isRowMatched(cleanRow, rows[i], filters) {
			
			// Thực hiện Update
			applyUpdateToRow(cacheData, i, updateData, deviceId, isDataTiktok)
			
			// Queue Update
			QueueUpdate(sid, sheetName, i, cacheData.RawValues[i])

			updatedCount++
			lastUpdatedIdx = i
			lastUpdatedRow = cacheData.RawValues[i]

			// Nếu là update đơn lẻ -> Dừng ngay sau khi tìm thấy nick đầu tiên
			if reqType == "updated" {
				break
			}
			// Nếu là updated_all -> Tiếp tục quét
		}
	}

	// --- KẾT QUẢ ---

	if updatedCount == 0 {
		return nil, fmt.Errorf("Không tìm thấy dữ liệu phù hợp")
	}

	if reqType == "updated_all" {
		return &UpdateResponse{
			Status: "true", Type: "updated_all",
			Messenger: fmt.Sprintf("Đã cập nhật thành công %d tài khoản", updatedCount),
			UpdatedCount: updatedCount,
		}, nil
	}

	// Trường hợp updated đơn lẻ
	return &UpdateResponse{
		Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
		RowIndex: RANGES.DATA_START_ROW + lastUpdatedIdx,
		AuthProfile: MakeAuthProfile(lastUpdatedRow),
		ActivityProfile: MakeActivityProfile(lastUpdatedRow),
		AiProfile: MakeAiProfile(lastUpdatedRow),
	}, nil
}

// =================================================================================================
// 🛠 CÁC HÀM HỖ TRỢ (PRIVATE HELPERS)
// =================================================================================================

// Hàm trích xuất dữ liệu cần update từ Body
func prepareUpdateData(body map[string]interface{}) map[int]interface{} {
	cols := make(map[int]interface{})
	
	// Quét các key col_X
	for k, v := range body {
		if strings.HasPrefix(k, "col_") {
			if idxStr := strings.TrimPrefix(k, "col_"); idxStr != "" {
				if idx, err := strconv.Atoi(idxStr); err == nil {
					cols[idx] = v
				}
			}
		}
	}
	
	// Map các key đặc biệt (status, note) vào index tương ứng
	if v, ok := body["status"]; ok { cols[INDEX_DATA_TIKTOK.STATUS] = v }
	if v, ok := body["note"]; ok { cols[INDEX_DATA_TIKTOK.NOTE] = v }
	
	return cols
}

// Hàm thực thi update lên 1 dòng cụ thể trong RAM (Và đồng bộ Map)
func applyUpdateToRow(cache *SheetCacheData, idx int, updateCols map[int]interface{}, deviceId string, isDataTiktok bool) {
	row := cache.RawValues[idx]
	cleanRow := cache.CleanValues[idx]

	// Lưu trạng thái cũ để so sánh
	oldStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
	oldDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]

	// 1. Áp dụng dữ liệu mới
	for colIdx, val := range updateCols {
		if colIdx >= 0 && colIdx < len(row) {
			row[colIdx] = val
			if colIdx < CACHE.CLEAN_COL_LIMIT {
				cleanRow[colIdx] = CleanString(val)
			}
		}
	}

	// 2. Xử lý Logic DataTiktok (Ghi chú, DeviceID, Map Sync)
	if isDataTiktok {
		// Update DeviceID nếu có yêu cầu (Chuyển quyền sở hữu)
		if deviceId != "" {
			row[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
			cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(deviceId)
		}

		// Xử lý Ghi chú (Note) giữ lịch sử
		// Lấy note cũ từ updateCols hoặc giữ nguyên
		content := ""
		if v, ok := updateCols[INDEX_DATA_TIKTOK.NOTE]; ok { content = fmt.Sprintf("%v", v) }
		
		// Note cũ trong dòng
		oldNoteInRow := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.NOTE]) 
		// Note này có thể vừa bị ghi đè ở bước 1, nhưng hàm tao_ghi_chu cần note gốc
		// Tuy nhiên logic đơn giản là nếu user gửi note mới -> Dùng note mới làm content
		
		newStatusRaw := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.STATUS])
		
		// Chỉ tạo format chuẩn nếu user có ý định update status hoặc note
		// Nếu updateCols không chứa status/note thì thôi, tránh spam note
		_, hasSt := updateCols[INDEX_DATA_TIKTOK.STATUS]
		_, hasNote := updateCols[INDEX_DATA_TIKTOK.NOTE]
		
		if hasSt || hasNote {
			finalNote := tao_ghi_chu_chuan_update(oldNoteInRow, content, newStatusRaw)
			row[INDEX_DATA_TIKTOK.NOTE] = finalNote
			cleanRow[INDEX_DATA_TIKTOK.NOTE] = CleanString(finalNote)
		}

		// 3. ĐỒNG BỘ MAP (QUAN TRỌNG)
		
		// Sync Status Map
		newStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
		if newStatus != oldStatus {
			removeFromStatusMap(cache.StatusMap, oldStatus, idx)
			cache.StatusMap[newStatus] = append(cache.StatusMap[newStatus], idx)
		}

		// Sync Assigned Map
		newDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
		if newDev != oldDev {
			// Xóa cũ
			if oldDev != "" {
				delete(cache.AssignedMap, oldDev)
			} else {
				removeFromIntList(&cache.UnassignedList, idx)
			}
			// Thêm mới
			if newDev != "" {
				cache.AssignedMap[newDev] = idx
			} else {
				cache.UnassignedList = append(cache.UnassignedList, idx)
			}
		}
	}
	
	// Update timestamp
	cache.LastAccessed = time.Now().UnixMilli()
}

// Hàm hỗ trợ xóa phần tử khỏi list int
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
