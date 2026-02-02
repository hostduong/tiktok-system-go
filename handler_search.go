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
   - Tìm kiếm dữ liệu và trả về kết quả đầy đủ (Full Columns) hoặc tùy chọn.
   - Luôn đảm bảo trả về chuỗi (String), không bao giờ null.

2. CẤU TRÚC BODY REQUEST:
{
  "token": "...",
  "sheet": "DataTiktok",
  "limit": 50,
  "return_cols": [],          // Rỗng = Lấy đủ 61 cột (0->60).

  "search_and": { ... },
  "search_or": { ... }
}

3. CẤU TRÚC RESPONSE:
{
    "status": "true",
    "messenger": "Thành công",
    "count": 1,
    "data": {
        "0": {
            "row_index": 15,
            "col_0": "...",
            ...
            "col_60": "" (Luôn có đủ key đến 60)
        }
    }
}
*/

type SearchResponse struct {
	Status    string                            `json:"status"`
	Messenger string                            `json:"messenger"`
	Count     int                               `json:"count"`
	Data      map[int]map[string]interface{}    `json:"data"`
}

func HandleSearchData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400); return
	}

	// 2. Xác thực
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }

	// 3. Tải dữ liệu
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Lỗi tải dữ liệu"})
		return
	}

	// 4. Phân tích bộ lọc
	filters := parseFilterParams(body)
	
	limit := 1000
	if l, ok := body["limit"]; ok {
		if val, ok := toFloat(l); ok && val > 0 { limit = int(val) }
	}

	var returnCols []int
	if v, ok := body["return_cols"]; ok {
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if val, ok := toFloat(item); ok { returnCols = append(returnCols, int(val)) }
			}
		}
	}
	sort.Ints(returnCols)
	fetchAll := (len(returnCols) == 0)

	// 5. Thực hiện tìm kiếm
	results := make(map[int]map[string]interface{})
	count := 0
	
	STATE.SheetMutex.RLock()
	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	
	// Xác định số cột tối đa cần lấy (Mặc định 61 cột theo cấu hình)
	maxColLimit := CACHE.CLEAN_COL_LIMIT // 61 (Từ 0 đến 60)

	for i, cleanRow := range cleanRows {
		if count >= limit { break }

		if isRowMatched(cleanRow, rows[i], filters) {
			
			item := make(map[string]interface{})
			item["row_index"] = i + RANGES.DATA_START_ROW
			
			rawRow := rows[i]
			
			if fetchAll {
				// 🔥 LOGIC MỚI: Chạy vòng lặp cố định từ 0 đến 60
				// Đảm bảo JSON luôn có đủ key col_0 -> col_60
				for colIdx := 0; colIdx < maxColLimit; colIdx++ {
					val := ""
					// Kiểm tra xem rawRow có dữ liệu tại index đó không
					if colIdx < len(rawRow) {
						val = SafeString(rawRow[colIdx])
					}
					// Gán vào map (Nếu rawRow thiếu thì val vẫn là "")
					item[fmt.Sprintf("col_%d", colIdx)] = val
				}
			} else {
				// Logic lấy theo cột chỉ định
				for _, colIdx := range returnCols {
					val := ""
					if colIdx >= 0 && colIdx < len(rawRow) {
						val = SafeString(rawRow[colIdx])
					}
					item[fmt.Sprintf("col_%d", colIdx)] = val
				}
			}
			
			results[count] = item
			count++
		}
	}
	STATE.SheetMutex.RUnlock()

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
