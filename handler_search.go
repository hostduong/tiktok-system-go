package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

/*
=================================================================================================
📘 TÀI LIỆU API: TÌM KIẾM DỮ LIỆU (POST /tool/search)
=================================================================================================

1. MỤC ĐÍCH:
   - Tìm kiếm dữ liệu trong Sheet dựa trên bộ lọc.
   - Trả về kết quả dạng Map (Object) để dễ truy xuất.

2. CẤU TRÚC BODY REQUEST:
{
  "token": "...",
  "sheet": "DataTiktok",      // (Optional) Tên sheet
  "limit": 50,                // (Optional) Giới hạn số dòng
  "return_cols": [],          // (Optional) Nếu RỖNG hoặc KHÔNG GỬI -> Lấy hết các cột.
                              // Nếu có gửi [0, 1, 6] -> Chỉ lấy cột 0, 1, 6.

  // --- BỘ LỌC CHUẨN ---
  "search_and": {
      "match_col_0": ["đang chạy"],
      "contains_col_6": ["@gmail.com"]
  },
  "search_or": { ... }
}

3. CẤU TRÚC RESPONSE (Dạng Map):
{
    "status": "true",
    "count": 2,
    "data": {
        "0": { "row_index": 15, "col_0": "...", "col_6": "..." },
        "1": { "row_index": 28, "col_0": "...", "col_6": "..." }
    }
}
*/

// Struct phản hồi kết quả tìm kiếm (Data là Map int -> Map string)
type SearchResponse struct {
	Status    string                            `json:"status"`
	Messenger string                            `json:"messenger"`
	Count     int                               `json:"count"`
	Data      map[int]map[string]interface{}    `json:"data"` // 🔥 Dùng Map theo yêu cầu
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
	filters := parseFilterParams(body) // Hàm từ utils.go
	
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
	// 🔥 Logic: Nếu returnCols rỗng -> fetchAll = true
	fetchAll := (len(returnCols) == 0)

	// 5. Thực hiện tìm kiếm (Scan)
	// Khởi tạo Map kết quả thay vì Slice
	results := make(map[int]map[string]interface{})
	count := 0
	
	STATE.SheetMutex.RLock() // Khóa đọc
	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	
	for i, cleanRow := range cleanRows {
		if count >= limit { break }

		// Kiểm tra điều kiện lọc
		if isRowMatched(cleanRow, rows[i], filters) {
			
			// Tạo object cho dòng này
			item := make(map[string]interface{})
			item["row_index"] = i + RANGES.DATA_START_ROW
			
			rawRow := rows[i]
			
			if fetchAll {
				// 🟢 TRƯỜNG HỢP 1: Lấy hết tất cả các cột có dữ liệu
				for colIdx, val := range rawRow {
					// Chỉ lấy các cột có giá trị để JSON gọn (hoặc lấy hết tùy ý, ở đây lấy hết)
					item[fmt.Sprintf("col_%d", colIdx)] = val
				}
			} else {
				// 🟢 TRƯỜNG HỢP 2: Chỉ lấy các cột trong return_cols
				for _, colIdx := range returnCols {
					if colIdx >= 0 && colIdx < len(rawRow) {
						item[fmt.Sprintf("col_%d", colIdx)] = rawRow[colIdx]
					}
				}
			}
			
			// Gán vào Map kết quả với key là số thứ tự 0, 1, 2...
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
			Status: "true", Messenger: "Lấy dữ liệu thành công", Count: count, Data: results,
		})
	}
}
