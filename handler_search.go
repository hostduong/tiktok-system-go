package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

/*
=================================================================================================
📘 TÀI LIỆU API: TÌM KIẾM DỮ LIỆU (POST /tool/search)
=================================================================================================

1. MỤC ĐÍCH:
   - Tìm kiếm dữ liệu trong Sheet theo bộ lọc.
   - Trả về dữ liệu an toàn (không bao giờ null).
   - Kết quả trả về dạng Map Object để Client dễ truy xuất theo Index.

2. CẤU TRÚC BODY REQUEST:
{
  "token": "...",
  "sheet": "DataTiktok",      // (Optional) Tên sheet
  "limit": 50,                // (Optional) Giới hạn số dòng
  "return_cols": [],          // (Optional) Nếu RỖNG -> Lấy hết. Nếu có [0, 6] -> Chỉ lấy cột 0 và 6.

  // --- BỘ LỌC CHUẨN ---
  "search_and": {
      "match_col_0": ["đang chạy"],
      "contains_col_6": ["@gmail.com"]
  },
  "search_or": { ... }
}

3. CẤU TRÚC RESPONSE (Key col_X luôn được sắp xếp dễ đọc):
{
    "status": "true",
    "messenger": "Thành công",
    "count": 1,
    "data": {
        "0": {
            "row_index": 15,
            "col_0": "Đang chạy",        // Luôn là string, không null
            "col_1": "",                 // Nếu rỗng trả về ""
            "col_6": "Tk_1|Pass_1"       // Giữ nguyên hoa thường
        }
    }
}
*/

// Struct phản hồi kết quả tìm kiếm
type SearchResponse struct {
	Status    string                            `json:"status"`
	Messenger string                            `json:"messenger"`
	Count     int                               `json:"count"`
	Data      map[int]map[string]interface{}    `json:"data"` // Dạng Map { "0": {...}, "1": {...} }
}

func HandleSearchData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400); return
	}

	// 2. Xác thực Token
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }

	// 3. Tải dữ liệu Cache
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Lỗi tải dữ liệu"})
		return
	}

	// 4. Phân tích tham số
	filters := parseFilterParams(body) // Dùng hàm chuẩn từ utils.go
	
	limit := 1000
	if l, ok := body["limit"]; ok {
		if val, ok := toFloat(l); ok && val > 0 { limit = int(val) }
	}

	// Xác định cột cần lấy (Projection)
	var returnCols []int
	if v, ok := body["return_cols"]; ok {
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if val, ok := toFloat(item); ok { returnCols = append(returnCols, int(val)) }
			}
		}
	}
	// Sắp xếp returnCols để dữ liệu trả về theo thứ tự cột tăng dần (đẹp mắt)
	sort.Ints(returnCols)
	
	fetchAll := (len(returnCols) == 0)

	// 5. Thực hiện tìm kiếm (Scan)
	results := make(map[int]map[string]interface{})
	count := 0
	
	STATE.SheetMutex.RLock() // Khóa đọc
	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	
	for i, cleanRow := range cleanRows {
		if count >= limit { break }

		// Kiểm tra điều kiện lọc
		if isRowMatched(cleanRow, rows[i], filters) {
			
			item := make(map[string]interface{})
			item["row_index"] = i + RANGES.DATA_START_ROW
			
			rawRow := rows[i]
			
			// 🔥 QUAN TRỌNG: Dùng SafeString để convert mọi thứ về String an toàn, giữ nguyên hoa thường
			
			if fetchAll {
				// Case 1: Lấy hết tất cả cột
				for colIdx, val := range rawRow {
					// SafeString: nil -> "", 123 -> "123", "AbC" -> "AbC"
					item[fmt.Sprintf("col_%d", colIdx)] = SafeString(val)
				}
			} else {
				// Case 2: Chỉ lấy cột yêu cầu
				for _, colIdx := range returnCols {
					val := ""
					if colIdx >= 0 && colIdx < len(rawRow) {
						val = SafeString(rawRow[colIdx])
					}
					// Dù cột đó không tồn tại trong data (Index Out of Range), vẫn trả về key đó với giá trị rỗng ""
					// Giúp Tool phía Client không bị crash do thiếu key.
					item[fmt.Sprintf("col_%d", colIdx)] = val
				}
			}
			
			results[count] = item
			count++
		}
	}
	STATE.SheetMutex.RUnlock() // Mở khóa

	// 6. Trả về kết quả
	if count == 0 {
		json.NewEncoder(w).Encode(SearchResponse{
			Status: "false", Messenger: "Không tìm thấy dữ liệu", Count: 0, Data: make(map[int]map[string]interface{}),
		})
	} else {
		json.NewEncoder(w).Encode(SearchResponse{
			Status: "true", Messenger: "Thành công", Count: count, Data: results,
		})
	}
}
