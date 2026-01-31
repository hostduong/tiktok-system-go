package main

import (
	"encoding/json"
	"net/http"
)

/*
=================================================================================================
📘 TÀI LIỆU API: TÌM KIẾM DỮ LIỆU (POST /tool/search)
=================================================================================================

1. MỤC ĐÍCH:
   - Tìm kiếm dữ liệu trong Sheet dựa trên nhiều điều kiện kết hợp.
   - Hỗ trợ lọc AND (tất cả phải đúng) và OR (một trong các điều kiện đúng).
   - Trả về kết quả dạng danh sách JSON.

2. CẤU TRÚC BODY REQUEST:
{
  "token": "...",
  "sheet": "DataTiktok",      // Tên sheet (Mặc định: DataTiktok)
  "limit": 50,                // Số lượng kết quả tối đa (Mặc định: 1000)
  "return_cols": [0, 1, 2, 6], // (Optional) Danh sách Index cột cần lấy. Nếu bỏ qua sẽ lấy hết.

  // --- ĐIỀU KIỆN LỌC (Dùng chung cấu trúc với Login/Update) ---
  "search_and": {
      "match_col_0": ["đang chạy"],       // Cột 0 chính xác là "đang chạy"
      "contains_col_6": ["@gmail.com"],   // Cột 6 chứa "@gmail.com"
      "min_col_29": 1000                  // Cột 29 >= 1000
  },
  "search_or": { ... }
}
*/

// Struct phản hồi kết quả tìm kiếm
type SearchResponse struct {
	Status    string                   `json:"status"`
	Messenger string                   `json:"messenger"`
	Count     int                      `json:"count"`
	Data      []map[string]interface{} `json:"data"` // Mảng kết quả
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

	// 4. Phân tích tham số tìm kiếm
	filters := parseFilterParams(body) // Dùng hàm chuẩn bên utils.go
	
	limit := 1000
	if l, ok := body["limit"]; ok {
		if val, ok := toFloat(l); ok && val > 0 { limit = int(val) }
	}

	// Xác định các cột cần trả về (Projection)
	var returnCols []int
	if v, ok := body["return_cols"]; ok {
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if val, ok := toFloat(item); ok { returnCols = append(returnCols, int(val)) }
			}
		}
	}

	// 5. Thực hiện tìm kiếm (Scan)
	var results []map[string]interface{}
	
	STATE.SheetMutex.RLock() // Khóa đọc
	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	
	for i, cleanRow := range cleanRows {
		if len(results) >= limit { break }

		// Sử dụng hàm so khớp chuẩn từ utils.go
		if isRowMatched(cleanRow, rows[i], filters) {
			
			// Tạo object kết quả cho dòng này
			item := make(map[string]interface{})
			item["row_index"] = i + RANGES.DATA_START_ROW // Luôn trả về row_index chuẩn
			
			// Lấy dữ liệu các cột
			rawRow := rows[i]
			if len(returnCols) > 0 {
				// Nếu chỉ yêu cầu một số cột nhất định
				for _, colIdx := range returnCols {
					if colIdx >= 0 && colIdx < len(rawRow) {
						key := fmt.Sprintf("col_%d", colIdx)
						item[key] = rawRow[colIdx]
					}
				}
			} else {
				// Lấy hết các cột (Mặc định)
				for colIdx, val := range rawRow {
					key := fmt.Sprintf("col_%d", colIdx)
					item[key] = val
				}
			}
			
			results = append(results, item)
		}
	}
	STATE.SheetMutex.RUnlock() // Mở khóa ngay sau khi quét xong

	// 6. Trả về kết quả
	if len(results) == 0 {
		json.NewEncoder(w).Encode(SearchResponse{
			Status: "false", Messenger: "Không tìm thấy dữ liệu", Count: 0, Data: []map[string]interface{}{},
		})
	} else {
		json.NewEncoder(w).Encode(SearchResponse{
			Status: "true", Messenger: "Thành công", Count: len(results), Data: results,
		})
	}
}
