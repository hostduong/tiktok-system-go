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

// =================================================================================================
// 🟢 CẤU TRÚC PHẢN HỒI (RESPONSE)
// =================================================================================================

type UpdateResponse struct {
	Status          string          `json:"status"`
	Type            string          `json:"type"`            // "updated" hoặc "updated_all"
	Messenger       string          `json:"messenger"`
	RowIndex        int             `json:"row_index,omitempty"`     // Dòng vừa sửa
	UpdatedCount    int             `json:"updated_count,omitempty"` // Số lượng dòng đã sửa
	AuthProfile     AuthProfile     `json:"auth_profile,omitempty"`     // Dữ liệu sau khi sửa
	ActivityProfile ActivityProfile `json:"activity_profile,omitempty"`
	AiProfile       AiProfile       `json:"ai_profile,omitempty"`
}

// =================================================================================================
// 🟢 HANDLER CHÍNH (Tiếp nhận Request)
// =================================================================================================

func HandleUpdateData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON từ Body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi định dạng JSON"}`, 400)
		return
	}

	// 2. Lấy Token từ Context (Middleware đã xác thực)
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

	// 5. Trả về kết quả JSON
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC LÕI (CORE LOGIC)
// =================================================================================================

func xu_ly_update_logic(sid, deviceId, reqType string, body map[string]interface{}) (*UpdateResponse, error) {
	// BƯỚC 1: Xác định Sheet cần thao tác
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	// BƯỚC 2: Tải dữ liệu từ Cache (Tối ưu tốc độ đọc)
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu hệ thống") }

	// BƯỚC 3: Phân tích bộ lọc (Filter)
	filters := parseFilterParams(body)
	
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok { rowIndexInput = int(val) }
	}

	// BƯỚC 4: Chuẩn bị dữ liệu Update (Chỉ lấy col_x)
	updateData := prepareUpdateData(body)
	if len(updateData) == 0 {
		return nil, fmt.Errorf("Không có dữ liệu để cập nhật (block 'updated' trống)")
	}

	// BƯỚC 5: KHÓA DỮ LIỆU (LOCK) - Bắt đầu quy trình ghi
	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues

	updatedCount := 0
	lastUpdatedIdx := -1
	var lastUpdatedRow []interface{}

	// --- CHIẾN LƯỢC A: CẬP NHẬT THEO ROW_INDEX (Trực tiếp & Nhanh nhất) ---
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		
		// Kiểm tra dòng có tồn tại hợp lệ không
		if idx >= 0 && idx < len(rows) {
			// Nếu có bộ lọc kèm theo, phải kiểm tra khớp mới cho sửa
			if filters.HasFilter {
				if !isRowMatched(cleanRows[idx], rows[idx], filters) {
					return nil, fmt.Errorf("Dữ liệu dòng %d không khớp điều kiện lọc", rowIndexInput)
				}
			}
			
			// Thực hiện Update vào RAM
			applyUpdateToRow(cacheData, idx, updateData, deviceId, isDataTiktok)
			
			// Đẩy xuống hàng đợi ghi đĩa (Async)
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

	// --- CHIẾN LƯỢC B: QUÉT TÌM VÀ CẬP NHẬT (Search & Update) ---
	if !filters.HasFilter {
		return nil, fmt.Errorf("Thiếu điều kiện tìm kiếm (cần row_index hoặc search_and/or)")
	}

	for i, cleanRow := range cleanRows {
		// Kiểm tra dòng có khớp bộ lọc không
		if isRowMatched(cleanRow, rows[i], filters) {
			
			// Cập nhật RAM
			applyUpdateToRow(cacheData, i, updateData, deviceId, isDataTiktok)
			
			// Cập nhật Đĩa
			QueueUpdate(sid, sheetName, i, cacheData.RawValues[i])

			updatedCount++
			lastUpdatedIdx = i
			lastUpdatedRow = cacheData.RawValues[i]

			// Nếu chế độ chỉ sửa 1 dòng -> Dừng ngay
			if reqType == "updated" { break }
		}
	}

	if updatedCount == 0 { return nil, fmt.Errorf("Không tìm thấy dữ liệu phù hợp") }

	// Phản hồi cho cập nhật hàng loạt
	if reqType == "updated_all" {
		return &UpdateResponse{
			Status: "true", Type: "updated_all",
			Messenger: fmt.Sprintf("Đã cập nhật thành công %d tài khoản", updatedCount),
			UpdatedCount: updatedCount,
		}, nil
	}

	// Phản hồi cho cập nhật đơn lẻ
	return &UpdateResponse{
		Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
		RowIndex: RANGES.DATA_START_ROW + lastUpdatedIdx,
		AuthProfile: MakeAuthProfile(lastUpdatedRow),
		ActivityProfile: MakeActivityProfile(lastUpdatedRow),
		AiProfile: MakeAiProfile(lastUpdatedRow),
	}, nil
}

// =================================================================================================
// 🛠 CÁC HÀM HỖ TRỢ (HELPER FUNCTIONS)
// =================================================================================================

// Lọc dữ liệu update từ JSON, chỉ chấp nhận key "col_X"
func prepareUpdateData(body map[string]interface{}) map[int]interface{} {
	cols := make(map[int]interface{})
	if v, ok := body["updated"]; ok {
		if updatedMap, ok := v.(map[string]interface{}); ok {
			for k, val := range updatedMap {
				// Chỉ nhận key bắt đầu bằng "col_" (Ví dụ: col_10)
				if strings.HasPrefix(k, "col_") {
					// Cắt lấy số Index phía sau
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

// Thực thi update vào RAM và đồng bộ các Map quản lý
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
			// Chỉ clean string nếu cột nằm trong vùng tìm kiếm (Tối ưu CPU)
			if colIdx < CACHE.CLEAN_COL_LIMIT {
				cleanRow[colIdx] = CleanString(val)
			}
		}
	}

	// 2. Logic riêng cho DataTiktok (Xử lý Note & Đồng bộ Map)
	if isDataTiktok {
		// Cập nhật DeviceId nếu có (Ưu tiên từ Root Request)
		if deviceId != "" {
			row[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
			cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(deviceId)
		}

		// --- XỬ LÝ NOTE THÔNG MINH (FIX LỖI MẤT SỐ LẦN) ---
		// Kiểm tra xem request có update Status hoặc Note không
		_, hasSt := updateCols[INDEX_DATA_TIKTOK.STATUS] 
		_, hasNote := updateCols[INDEX_DATA_TIKTOK.NOTE]
		
		if hasSt || hasNote {
			// Lấy nội dung note mới (nếu có)
			content := ""
			if v, ok := updateCols[INDEX_DATA_TIKTOK.NOTE]; ok { content = fmt.Sprintf("%v", v) }
			
			// Lấy dữ liệu cũ (ĐỂ TRÍCH XUẤT SỐ LẦN CHẠY)
			oldNoteInRow := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.NOTE]) 
			newStatusRaw := fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.STATUS])
			
			// Tạo note chuẩn (Giữ nguyên số lần bằng Regex)
			finalNote := tao_ghi_chu_chuan_update(oldNoteInRow, content, newStatusRaw)
			
			// Ghi đè lại
			row[INDEX_DATA_TIKTOK.NOTE] = finalNote
			cleanRow[INDEX_DATA_TIKTOK.NOTE] = CleanString(finalNote)
		}

		// --- ĐỒNG BỘ RAM (QUAN TRỌNG) ---
		
		// 1. Đồng bộ StatusMap (Để tìm nick theo trạng thái)
		newStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
		if newStatus != oldStatus {
			removeFromStatusMap(cache.StatusMap, oldStatus, idx)
			cache.StatusMap[newStatus] = append(cache.StatusMap[newStatus], idx)
		}

		// 2. Đồng bộ AssignedMap (Để tìm nick theo thiết bị)
		newDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
		if newDev != oldDev {
			// Xóa khỏi vị trí cũ
			if oldDev != "" { 
				delete(cache.AssignedMap, oldDev) 
			} else { 
				// ⚠️ Dùng hàm removeFromIntList (Có trong utils.go, vì cùng package main nên gọi được)
				removeFromIntList(cache.UnassignedList, idx) 
			}
			// Thêm vào vị trí mới
			if newDev != "" { 
				cache.AssignedMap[newDev] = idx 
			} else { 
				// ⚠️ Dùng logic append trực tiếp
				cache.UnassignedList = append(cache.UnassignedList, idx) 
			}
		}
	}
	
	cache.LastAccessed = time.Now().UnixMilli()
}

// Logic tạo Note Update: Dùng Regex để giữ nguyên số lần chạy cũ
func tao_ghi_chu_chuan_update(oldNote, content, newStatus string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	
	// 🔥 QUAN TRỌNG: Làm sạch note cũ để Regex hoạt động chuẩn
	oldNote = SafeString(oldNote) 
	
	count := 1 
	// 🔥 Dùng Regex bắt số lần từ note cũ (Chính xác 100%)
	match := REGEX_COUNT.FindStringSubmatch(oldNote)
	if len(match) > 1 {
		if c, err := strconv.Atoi(match[1]); err == nil {
			count = c // TÌM THẤY -> GIỮ NGUYÊN SỐ LẦN NÀY
		}
	}

	// Ưu tiên nội dung gửi lên -> nếu không thì dùng status -> nếu không thì giữ dòng cũ
	statusToUse := content
	if statusToUse == "" { statusToUse = newStatus }
	
	// Nếu vẫn rỗng, cố gắng lấy dòng đầu của note cũ (giữ trạng thái cũ)
	if statusToUse == "" {
		lines := strings.Split(oldNote, "\n")
		if len(lines) > 0 { statusToUse = lines[0] }
	}
	
	// Fallback cuối cùng
	if statusToUse == "" { statusToUse = "Đang chạy" }

	return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
}
