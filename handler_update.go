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
   - Cập nhật thông tin tài khoản (Trạng thái, Ghi chú, Cookie, Proxy...) vào hệ thống.
   - Hỗ trợ cập nhật 1 dòng (updated) hoặc nhiều dòng (updated_all).
   - Tự động đồng bộ RAM để các tiến trình khác (Login/Search) nhận diện thay đổi ngay lập tức.
   - 🔥 ĐẶC BIỆT: Khi cập nhật Note, hệ thống sẽ BẢO TOÀN số lần chạy cũ (Ví dụ: "Lần 5" -> "Lần 6").

2. CẤU TRÚC BODY REQUEST CHI TIẾT:
{
  "type": "updated",          // Lệnh: "updated" (Sửa 1 dòng tìm thấy đầu tiên) 
                              // hoặc "updated_all" (Sửa tất cả dòng tìm thấy)
  "token": "...",             // Token xác thực (Bắt buộc)
  "sheet": "DataTiktok",      // (Tùy chọn) Tên sheet đích. Mặc định: DataTiktok.
  
  // --- TÙY CHỌN 1: CẬP NHẬT CHÍNH XÁC (Ưu tiên số 1) ---
  "row_index": 123,           // Cập nhật chính xác dòng 123 (Index tính từ 0 của Excel)
  
  // --- TÙY CHỌN 2: BỘ LỌC DỮ LIỆU (Dùng khi không biết row_index) ---
  "search_and": {             // Điều kiện VÀ (Tất cả phải đúng)
      "match_col_0": ["đang chạy"],       // Cột 0 (Status) phải là "đang chạy"
      "match_col_2": ["device_id_cu"],    // Cột 2 (DeviceId) phải khớp ID cũ
      "contains_col_6": ["@gmail.com"]    // Cột 6 (Email) chứa "@gmail.com"
  },
  
  // --- PHẦN DỮ LIỆU CẦN SỬA (UPDATED BLOCK) ---
  // Muốn sửa cột nào, bắt buộc phải khai báo ở đây (Dạng col_X).
  "updated": {
      "col_0": "Đang chạy",              // Sửa Status
      "col_1": "Cookie die",             // Sửa Note (Hệ thống sẽ tự thêm ngày giờ và số lần chạy)
      "col_2": "device_id_moi",          // Muốn đổi chủ (DeviceID) thì gửi giá trị mới ở đây
      "col_17": "cookie_mới_ads..."      // Sửa Cookie
  }
}
*/

// Cấu trúc phản hồi JSON
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

// =================================================================================================
// 🟢 HANDLER CHÍNH
// =================================================================================================

func HandleUpdateData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi cấu trúc JSON"}`, 400); return
	}

	// 2. Xác thực Token
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	reqType := CleanString(body["type"])
	if reqType == "" { reqType = "updated" } // Mặc định là updated 1 dòng

	// 3. Gọi hàm xử lý Logic (Đã bỏ tham số deviceId thừa)
	res, err := xu_ly_update_logic(sid, reqType, body)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC LÕI (CORE BUSINESS LOGIC)
// =================================================================================================

func xu_ly_update_logic(sid, reqType string, body map[string]interface{}) (*UpdateResponse, error) {
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	// 1. Tải dữ liệu từ RAM (Nhanh, không gọi Google)
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu: %v", err) }

	filters := parseFilterParams(body)
	
	// Check Row Index input
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok { if val, ok := toFloat(v); ok { rowIndexInput = int(val) } }

	// Parse dữ liệu cần update
	updateData := prepareUpdateData(body)
	if len(updateData) == 0 { return nil, fmt.Errorf("Dữ liệu updated trống") }

	// KHÓA GHI (LOCK) TOÀN BỘ QUÁ TRÌNH UPDATE ĐỂ ĐẢM BẢO AN TOÀN
	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	updatedCount := 0
	lastUpdatedIdx := -1
	var lastUpdatedRow []interface{}

	// =========================================================================================
	// 🎯 TRƯỜNG HỢP 1: UPDATE THEO ROW INDEX (Ưu tiên Tuyệt đối)
	// =========================================================================================
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		// Kiểm tra biên an toàn
		if idx >= 0 && idx < len(rows) {
			// Check Filter nếu có (Để đảm bảo an toàn, ví dụ: chỉ update nếu đúng là nick của mình)
			if filters.HasFilter {
				if !isRowMatched(cleanRows[idx], rows[idx], filters) { return nil, fmt.Errorf("Dòng %d không khớp bộ lọc bảo vệ", rowIndexInput) }
			}
			
			// Thực hiện Update
			applyUpdateToRow(cacheData, idx, updateData, isDataTiktok)
			
			// Đẩy xuống Queue ghi đĩa
			newRow := make([]interface{}, len(cacheData.RawValues[idx])); copy(newRow, cacheData.RawValues[idx])
			QueueUpdate(sid, sheetName, idx, newRow)
			
			return &UpdateResponse{
				Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
				RowIndex: rowIndexInput,
				AuthProfile: MakeAuthProfile(cacheData.RawValues[idx]),
				ActivityProfile: MakeActivityProfile(cacheData.RawValues[idx]),
				AiProfile: MakeAiProfile(cacheData.RawValues[idx]),
			}, nil
		} else { return nil, fmt.Errorf("Dòng %d không tồn tại", rowIndexInput) }
	}

	// =========================================================================================
	// 🎯 TRƯỜNG HỢP 2: UPDATE THEO BỘ LỌC (SEARCH & UPDATE)
	// =========================================================================================
	if !filters.HasFilter { return nil, fmt.Errorf("Thiếu điều kiện tìm kiếm (search_and/search_or)") }

	for i, cleanRow := range cleanRows {
		// Kiểm tra khớp bộ lọc
		if isRowMatched(cleanRow, rows[i], filters) {
			
			// Thực hiện Update
			applyUpdateToRow(cacheData, i, updateData, isDataTiktok)
			
			// Đẩy xuống Queue
			newRow := make([]interface{}, len(cacheData.RawValues[i])); copy(newRow, cacheData.RawValues[i])
			QueueUpdate(sid, sheetName, i, newRow)
			
			updatedCount++
			lastUpdatedIdx = i
			lastUpdatedRow = cacheData.RawValues[i]
			
			// Nếu lệnh là "updated" (1 dòng) -> Dừng ngay sau khi sửa xong dòng đầu tiên
			if reqType == "updated" { break }
		}
	}

	if updatedCount == 0 { return nil, fmt.Errorf("Không tìm thấy dòng nào phù hợp") }

	// Trả về kết quả cho updated_all
	if reqType == "updated_all" {
		return &UpdateResponse{
			Status: "true", Type: "updated_all",
			Messenger: fmt.Sprintf("Đã cập nhật %d tài khoản", updatedCount), UpdatedCount: updatedCount,
		}, nil
	}

	// Trả về kết quả cho updated (1 dòng)
	return &UpdateResponse{
		Status: "true", Type: "updated", Messenger: "Cập nhật thành công",
		RowIndex: RANGES.DATA_START_ROW + lastUpdatedIdx,
		AuthProfile: MakeAuthProfile(lastUpdatedRow), ActivityProfile: MakeActivityProfile(lastUpdatedRow), AiProfile: MakeAiProfile(lastUpdatedRow),
	}, nil
}

// =================================================================================================
// 🛠️ CÁC HÀM HỖ TRỢ (HELPER FUNCTIONS)
// =================================================================================================

// Parse dữ liệu "updated" từ JSON
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

// Hàm thực hiện update vào RAM (Critical Function)
func applyUpdateToRow(cache *SheetCacheData, idx int, updateCols map[int]interface{}, isDataTiktok bool) {
	row := cache.RawValues[idx]
	cleanRow := cache.CleanValues[idx]
	
	// Lưu trạng thái cũ để so sánh sự thay đổi (Dùng cho Sync Map)
	oldStatus := ""
	oldDev := ""
	if isDataTiktok {
		oldStatus = cleanRow[INDEX_DATA_TIKTOK.STATUS]
		oldDev = cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
	}

	// 🔥 QUAN TRỌNG: LẤY NOTE CŨ RA TRƯỚC KHI VÒNG LẶP UPDATE CHẠY
	// Fix lỗi: Nếu chạy vòng lặp trước, note cũ sẽ bị đè mất -> Hàm tạo note sẽ tưởng là note mới -> Reset số lần chạy về 1.
	realOldNote := ""
	if isDataTiktok {
		realOldNote = fmt.Sprintf("%v", row[INDEX_DATA_TIKTOK.NOTE])
	}

	// 1. Áp dụng dữ liệu mới vào Row
	for colIdx, val := range updateCols {
		// Kiểm tra biên mảng (Tránh Panic nếu người dùng gửi col_1000)
		if colIdx >= 0 && colIdx < len(row) {
			row[colIdx] = val
			// Cập nhật cả CleanValues để Search sau này thấy ngay
			if colIdx < CACHE.CLEAN_COL_LIMIT { cleanRow[colIdx] = CleanString(val) }
		}
	}

	// 2. Logic Riêng cho DataTiktok (Đồng bộ Map & Xử lý Note)
	if isDataTiktok {
		// Xử lý Note Logic (Giữ số lần chạy)
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

		// 🔥 ĐỒNG BỘ RAM (SYNC MAPS) - Giúp Login/Search thấy ngay sự thay đổi
		
		// 2.1. Đồng bộ Status Map
		newStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
		if newStatus != oldStatus {
			removeFromStatusMap(cache.StatusMap, oldStatus, idx)
			cache.StatusMap[newStatus] = append(cache.StatusMap[newStatus], idx)
		}
		
		// 2.2. Đồng bộ Assigned Map (DeviceID)
		newDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
		if newDev != oldDev {
			// Xóa khỏi nhóm cũ
			if oldDev != "" { delete(cache.AssignedMap, oldDev) } else { removeFromIntList(&cache.UnassignedList, idx) }
			// Thêm vào nhóm mới
			if newDev != "" { cache.AssignedMap[newDev] = idx } else { cache.UnassignedList = append(cache.UnassignedList, idx) }
		}
	}
	
	// Đánh dấu thời gian truy cập để Cache không bị dọn dẹp sớm
	cache.LastAccessed = time.Now().UnixMilli()
}

// Logic tạo Note UPDATE: GIỮ NGUYÊN số lần chạy
func tao_ghi_chu_chuan_update(oldNote, content, newStatus string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	oldNote = SafeString(oldNote)
	count := 1
	
	// Bắt số lần từ note cũ: "(Lần 5)"
	match := REGEX_COUNT.FindStringSubmatch(oldNote)
	if len(match) > 1 { if c, err := strconv.Atoi(match[1]); err == nil { count = c } }

	// Ưu tiên nội dung content mới, nếu không có thì lấy status mới
	statusToUse := content
	if statusToUse == "" { statusToUse = newStatus }
	
	// Nếu vẫn rỗng, cố gắng giữ lại dòng trạng thái cũ đầu tiên (để không bị mất thông tin cũ)
	if statusToUse == "" {
		lines := strings.Split(oldNote, "\n")
		if len(lines) > 0 { statusToUse = lines[0] }
	}
	if statusToUse == "" { statusToUse = "Đang chạy" }

	return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
}
