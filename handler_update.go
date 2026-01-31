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

1. MỤC ĐÍCH: Cập nhật thông tin tài khoản (Trạng thái, Ghi chú, Cookie,...) vào hệ thống và Excel.

2. CẤU TRÚC BODY REQUEST:
{
  "type": "updated",          // Loại lệnh: "updated" (1 dòng) hoặc "updated_all" (nhiều dòng)
  "token": "...",             // Token xác thực
  "deviceId": "...",          // ID thiết bị (để map dữ liệu nếu cần)
  "sheet": "DataTiktok",      // (Tùy chọn) Tên sheet, mặc định là DataTiktok
  
  // --- PHẦN 1: ĐIỀU KIỆN TÌM KIẾM (FILTER) ---
  "row_index": 123,           // (Ưu tiên 1) Cập nhật chính xác dòng số 123 (Index tính từ 0 của Excel)
  
  "search_and": {             // (Ưu tiên 2) Tìm dòng thỏa mãn TẤT CẢ điều kiện
      "match_col_0": ["đang chạy"],       // Cột 0 phải là "đang chạy"
      "contains_col_6": ["@gmail.com"]    // Cột 6 phải chứa "@gmail.com"
  },
  
  // --- PHẦN 2: DỮ LIỆU CẬP NHẬT (UPDATED BLOCK) ---
  // QUY TẮC: Chỉ sử dụng key dạng "col_X" (X là số thứ tự cột, bắt đầu từ 0)
  "updated": {
      "col_0": "Đang chạy",              // Cập nhật Cột 0 (Status)
      "col_1": "Nội dung ghi chú mới",   // Cập nhật Cột 1 (Note)
      "col_17": "cookie_mới_ở_đây"       // Cập nhật Cột 17 (Cookie)
  }
}

3. QUY TẮC CỘT QUAN TRỌNG (DataTiktok):
- col_0: Status (Trạng thái)
- col_1: Note (Ghi chú)
- col_2: DeviceId
- col_6: Email
- col_8: Password
*/

// =================================================================================================
// 🟢 CẤU TRÚC PHẢN HỒI (RESPONSE)
// =================================================================================================

type UpdateResponse struct {
	Status          string          `json:"status"`
	Type            string          `json:"type"`            // "updated" hoặc "updated_all"
	Messenger       string          `json:"messenger"`
	RowIndex        int             `json:"row_index,omitempty"`     // Trả về dòng vừa sửa (nếu sửa 1 dòng)
	UpdatedCount    int             `json:"updated_count,omitempty"` // Trả về số lượng dòng đã sửa (nếu sửa nhiều)
	AuthProfile     AuthProfile     `json:"auth_profile,omitempty"`     // Profile sau khi sửa
	ActivityProfile ActivityProfile `json:"activity_profile,omitempty"` // Chỉ số hoạt động sau khi sửa
	AiProfile       AiProfile       `json:"ai_profile,omitempty"`       // Cấu hình AI sau khi sửa
}

// =================================================================================================
// 🟢 HANDLER CHÍNH (Tiếp nhận Request)
// =================================================================================================

func HandleUpdateData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON Body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi định dạng JSON"}`, 400)
		return
	}

	// 2. Lấy Context xác thực (Token)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	// 3. Chuẩn hóa dữ liệu đầu vào
	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	// Mặc định là updated (sửa 1 dòng) nếu không gửi type
	if reqType == "" { reqType = "updated" }

	// 4. Gọi hàm xử lý logic
	res, err := xu_ly_update_logic(sid, deviceId, reqType, body)

	// 5. Trả về kết quả
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC LÕI (Xử lý nghiệp vụ)
// =================================================================================================

func xu_ly_update_logic(sid, deviceId, reqType string, body map[string]interface{}) (*UpdateResponse, error) {
	// BƯỚC 1: Xác định Sheet cần làm việc
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	// BƯỚC 2: Tải dữ liệu từ Cache (Rất nhanh)
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu hệ thống") }

	// BƯỚC 3: Phân tích bộ lọc (Filter) từ Request
	filters := parseFilterParams(body)
	
	// Kiểm tra xem có gửi row_index trực tiếp không
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok { rowIndexInput = int(val) }
	}

	// BƯỚC 4: Chuẩn bị dữ liệu cần Update (Chỉ lấy col_x)
	updateData := prepareUpdateData(body)
	if len(updateData) == 0 {
		return nil, fmt.Errorf("Không có dữ liệu để cập nhật (block 'updated' trống hoặc sai định dạng)")
	}

	// BƯỚC 5: KHÓA DỮ LIỆU (LOCK) - Bắt đầu thay đổi dữ liệu
	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues

	updatedCount := 0
	lastUpdatedIdx := -1
	var lastUpdatedRow []interface{}

	// --- CHIẾN LƯỢC A: CẬP NHẬT THEO ROW_INDEX (Nhanh nhất) ---
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		
		// Kiểm tra dòng có tồn tại không
		if idx >= 0 && idx < len(rows) {
			// Nếu có thêm bộ lọc, phải kiểm tra dòng đó có khớp không
			if filters.HasFilter {
				if !isRowMatched(cleanRows[idx], rows[idx], filters) {
					return nil, fmt.Errorf("Dữ liệu dòng %d không khớp điều kiện lọc kèm theo", rowIndexInput)
				}
			}
			
			// Thực hiện Update
			applyUpdateToRow(cacheData, idx, updateData, deviceId, isDataTiktok)
			
			// Ghi xuống đĩa (Bất đồng bộ)
			QueueUpdate(sid, sheetName, idx, cacheData.RawValues[idx])
			
			return &UpdateResponse{
				Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
				RowIndex: rowIndexInput,
				AuthProfile: MakeAuthProfile(cacheData.RawValues[idx]),
				ActivityProfile: MakeActivityProfile(cacheData.RawValues[idx]),
				AiProfile: MakeAiProfile(cacheData.RawValues[idx]),
			}, nil
		} else {
			return nil, fmt.Errorf("Dòng yêu cầu không tồn tại trong dữ liệu")
		}
	}

	// --- CHIẾN LƯỢC B: QUÉT TÌM VÀ CẬP NHẬT (Search & Update) ---
	if !filters.HasFilter {
		return nil, fmt.Errorf("Thiếu điều kiện tìm kiếm (cần row_index hoặc search_and/or)")
	}

	for i, cleanRow := range cleanRows {
		// Kiểm tra dòng có khớp bộ lọc không
		if isRowMatched(cleanRow, rows[i], filters) {
			
			// Thực hiện Update
			applyUpdateToRow(cacheData, i, updateData, deviceId, isDataTiktok)
			
			// Ghi xuống đĩa
			QueueUpdate(sid, sheetName, i, cacheData.RawValues[i])

			updatedCount++
			lastUpdatedIdx = i
			lastUpdatedRow = cacheData.RawValues[i]

			// Nếu chỉ yêu cầu update 1 dòng -> Dừng ngay sau khi tìm thấy
			if reqType == "updated" { break }
		}
	}

	if updatedCount == 0 { return nil, fmt.Errorf("Không tìm thấy dữ liệu phù hợp với bộ lọc") }

	// Trả về kết quả cho updated_all
	if reqType == "updated_all" {
		return &UpdateResponse{
			Status: "true", Type: "updated_all",
			Messenger: fmt.Sprintf("Đã cập nhật thành công %d tài khoản", updatedCount),
			UpdatedCount: updatedCount,
		}, nil
	}

	// Trả về kết quả cho updated (1 dòng)
	return &UpdateResponse{
		Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
		RowIndex: RANGES.DATA_START_ROW + lastUpdatedIdx,
		AuthProfile: MakeAuthProfile(lastUpdatedRow),
		ActivityProfile: MakeActivityProfile(lastUpdatedRow),
		AiProfile: MakeAiProfile(lastUpdatedRow),
	}, nil
}

// =================================================================================================
// 🛠 HÀM BỔ TRỢ (HELPER FUNCTIONS)
// =================================================================================================

// Chuẩn bị map dữ liệu update từ JSON, chỉ chấp nhận key "col_X"
func prepareUpdateData(body map[string]interface{}) map[int]interface{} {
	cols := make(map[int]interface{})
	
	// Vào block "updated"
	if v, ok := body["updated"]; ok {
		if updatedMap, ok := v.(map[string]interface{}); ok {
			for k, val := range updatedMap {
				// Chỉ quét các key bắt đầu bằng "col_"
				if strings.HasPrefix(k, "col_") {
					// Cắt lấy số Index phía sau (Ví dụ: col_10 -> 10)
					if idxStr := strings.TrimPrefix(k, "col_"); idxStr != "" {
						if idx, err := strconv.Atoi(idxStr); err == nil {
							cols[idx] = val
						}
					}
				}
			}
		}
	}
	return cols
}

// Hàm thực thi update vào 1 dòng cụ thể trong Cache
func applyUpdateToRow(cache *SheetCacheData, idx int, updateCols map[int]interface{}, deviceId string, isDataTiktok bool) {
	row := cache.RawValues[idx]
	cleanRow := cache.CleanValues[idx]

	// Lưu lại trạng thái cũ để so sánh
	oldStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
	oldDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]

	// 1. Áp dụng dữ liệu mới vào các cột
	for colIdx, val := range updateCols {
		if colIdx >= 0 && colIdx < len(row) {
			row[colIdx] = val
			// Chỉ clean string nếu cột đó nằm trong phạm vi tìm kiếm (Tối ưu tốc độ)
			if colIdx < CACHE.CLEAN_COL_LIMIT {
				cleanRow[colIdx] = CleanString(val)
			}
		}
	}

	// 2. Logic riêng cho Sheet DataTiktok (Đồng bộ Map, xử lý Note)
	if isDataTiktok {
		// Nếu có DeviceId trong request, cập nhật luôn vào cột
		if deviceId != "" {
			row[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
			cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(deviceId)
		}

		// --- XỬ LÝ GHI CHÚ THÔNG MINH ---
		// Kiểm tra xem request có update cột Status (0) hoặc Note (1) không
		_, hasSt := updateCols[INDEX_DATA_TIKTOK.STATUS] 
		_, hasNote := updateCols[INDEX_DATA_TIKTOK.NOTE]
		
		if hasSt || hasNote {
			// Lấy nội dung note mới (nếu có gửi lên)
			content := ""
			if v, ok := updateCols[INDEX_DATA_TIKTOK.NOTE]; ok { content = fmt.Sprintf("%v", v) }
			
			// Lấy dữ liệu hiện tại để tính toán
			oldNoteInRow := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.NOTE]) 
			newStatusRaw := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.STATUS])
			
			// Tạo note chuẩn (Giữ nguyên số lần chạy bằng Regex)
			finalNote := tao_ghi_chu_chuan_update(oldNoteInRow, content, newStatusRaw)
			
			// Ghi đè lại cột Note
			row[INDEX_DATA_TIKTOK.NOTE] = finalNote
			cleanRow[INDEX_DATA_TIKTOK.NOTE] = CleanString(finalNote)
		}

		// --- ĐỒNG BỘ CACHE MAP (Để tìm kiếm nhanh) ---
		
		// 1. Đồng bộ StatusMap (Nếu status đổi, phải chuyển index sang nhóm mới)
		newStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
		if newStatus != oldStatus {
			removeFromStatusMap(cache.StatusMap, oldStatus, idx)
			cache.StatusMap[newStatus] = append(cache.StatusMap[newStatus], idx)
		}

		// 2. Đồng bộ AssignedMap (Quản lý thiết bị)
		newDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
		if newDev != oldDev {
			// Xóa khỏi map cũ
			if oldDev != "" { 
				delete(cache.AssignedMap, oldDev) 
			} else { 
				removeFromIntList(&cache.UnassignedList, idx) 
			}
			// Thêm vào map mới
			if newDev != "" { 
				cache.AssignedMap[newDev] = idx 
			} else { 
				cache.UnassignedList = append(cache.UnassignedList, idx) 
			}
		}
	}
	
	// Cập nhật thời gian truy cập
	cache.LastAccessed = time.Now().UnixMilli()
}

// Hàm xóa 1 phần tử khỏi mảng int
func removeFromIntList(list *[]int, target int) {
	for i, v := range *list {
		if v == target {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return
		}
	}
}

// Logic tạo note update: Dùng Regex bắt số lần -> GIỮ NGUYÊN SỐ LẦN
func tao_ghi_chu_chuan_update(oldNote, content, newStatus string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	oldNote = SafeString(oldNote) 
	
	count := 1 
	// Dùng Regex lấy số lần (Đảm bảo chính xác 100%)
	match := REGEX_COUNT.FindStringSubmatch(oldNote)
	if len(match) > 1 {
		if c, err := strconv.Atoi(match[1]); err == nil {
			count = c // Giữ nguyên số lần tìm được
		}
	}

	// Ưu tiên nội dung note gửi lên, nếu không có thì dùng status mới
	statusToUse := content
	if statusToUse == "" { statusToUse = newStatus }
	
	// Nếu vẫn rỗng, cố gắng giữ lại dòng trạng thái cũ
	if statusToUse == "" {
		lines := strings.Split(oldNote, "\n")
		if len(lines) > 0 { statusToUse = lines[0] }
	}
	// Fallback cuối cùng
	if statusToUse == "" { statusToUse = "Đang chạy" }

	return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
}
