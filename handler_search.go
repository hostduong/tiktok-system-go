package main

import (
	"bytes"
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
   - Tìm kiếm dữ liệu trong Sheet (mặc định DataTiktok) với tốc độ cao (RAM).
   - Trả về JSON với thứ tự cột ĐƯỢC SẮP XẾP CHUẨN (row_index -> col_0 -> col_1...).
   - Hỗ trợ lấy toàn bộ cột hoặc chỉ lấy cột chỉ định.

2. CẤU TRÚC BODY REQUEST:
{
  "token": "...",             // Token xác thực
  "sheet": "DataTiktok",      // (Tùy chọn) Tên sheet cần tìm. Mặc định: DataTiktok.
  "limit": 50,                // (Tùy chọn) Giới hạn số dòng trả về. Mặc định: 1000.
  "return_cols": [0, 1, 2],   // (Tùy chọn) Chỉ lấy cột 0, 1, 2. Nếu Rỗng = Lấy đủ 61 cột.

  // --- ĐIỀU KIỆN TÌM KIẾM (FILTER) ---
  "search_and": {
      "match_col_0": ["đang chạy"],   // Cột 0 phải là "đang chạy"
      "contains_col_6": ["@gmail"]    // Cột 6 chứa "@gmail"
  },
  "search_or": { ... }
}

3. CẤU TRÚC RESPONSE (JSON Ordered):
{
    "status": "true",
    "messenger": "Thành công",
    "count": 1,
    "data": {
        "0": {
            "row_index": 15,     <-- LUÔN ĐỨNG ĐẦU TIÊN
            "col_0": "...",
            "col_1": "...",
            "col_2": "...",      <-- THỨ TỰ TĂNG DẦN (col_2 trước col_10)
            ...
        }
    }
}
*/

// =================================================================================================
// 🟢 STRUCT TÙY BIẾN ĐỂ SẮP XẾP JSON (CUSTOM MARSHALER)
// =================================================================================================

// OrderedRow: Struct đại diện cho 1 dòng, dùng để Custom JSON
type OrderedRow struct {
	RowIndex int            // Số dòng thực tế trong Excel (Tính từ 0 + Header)
	Columns  map[int]string // Dữ liệu các cột (Key là số int)
}

// MarshalJSON: Hàm này sẽ được gọi tự động khi json.Encode.
// Tại đây chúng ta TỰ TAY viết chuỗi JSON để ép nó theo thứ tự mong muốn.
func (r *OrderedRow) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	
	// 1. Mở ngoặc nhọn và ghi row_index đầu tiên (Quan trọng nhất để Client biết dòng nào)
	buf.WriteString(fmt.Sprintf(`{"row_index":%d`, r.RowIndex))

	// 2. Lấy danh sách Key cột và Sắp xếp (Để đảm bảo duyệt từ 0 -> 60)
	// (Map trong Go duyệt ngẫu nhiên, nên bước sort này là bắt buộc nếu muốn JSON đẹp)
	keys := make([]int, 0, len(r.Columns))
	for k := range r.Columns {
		keys = append(keys, k)
	}
	sort.Ints(keys) // Sắp xếp key tăng dần: 0, 1, 2, ..., 10, 11...

	// 3. Duyệt và ghi vào buffer
	for _, i := range keys {
		val := r.Columns[i]
		// Marshal value để đảm bảo các ký tự đặc biệt (ngoặc kép, xuống dòng) được escape đúng chuẩn JSON
		valJson, _ := json.Marshal(val)
		
		// Ghi vào buffer: ,"col_X":"giá trị"
		buf.WriteString(fmt.Sprintf(`,"col_%d":%s`, i, valJson))
	}

	// 4. Đóng ngoặc nhọn
	buf.WriteString("}")
	return buf.Bytes(), nil
}

// =================================================================================================
// 🟢 CẤU TRÚC PHẢN HỒI CHÍNH
// =================================================================================================

type SearchResponse struct {
	Status    string                `json:"status"`
	Messenger string                `json:"messenger"`
	Count     int                   `json:"count"`
	Data      map[int]*OrderedRow   `json:"data"` // Key là số thứ tự (0, 1, 2...)
}

// =================================================================================================
// 🟢 HANDLER CHÍNH
// =================================================================================================

func HandleSearchData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi cấu trúc JSON"}`, 400); return
	}

	// 2. Xác thực Token
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	sheetName := CleanString(body["sheet"])
	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }

	// 3. Tải dữ liệu từ RAM
	cacheData, err := LayDuLieu(sid, sheetName, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Lỗi tải dữ liệu"})
		return
	}

	// 4. Phân tích tham số
	filters := parseFilterParams(body)
	
	limit := 1000 // Mặc định lấy tối đa 1000 dòng
	if l, ok := body["limit"]; ok {
		if val, ok := toFloat(l); ok && val > 0 { limit = int(val) }
	}

	// Xác định cột cần lấy
	var returnCols []int
	if v, ok := body["return_cols"]; ok {
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if val, ok := toFloat(item); ok { returnCols = append(returnCols, int(val)) }
			}
		}
	}
	sort.Ints(returnCols) // Sắp xếp cột yêu cầu
	fetchAll := (len(returnCols) == 0)

	// 5. Thực hiện tìm kiếm (RAM Scan)
	results := make(map[int]*OrderedRow) // Map kết quả
	count := 0
	
	STATE.SheetMutex.RLock() // Khóa ĐỌC
	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	
	maxColLimit := CACHE.CLEAN_COL_LIMIT // Mặc định 61 cột

	for i, cleanRow := range cleanRows {
		// Nếu đã đủ số lượng limit -> Dừng tìm
		if count >= limit { break }

		// Kiểm tra khớp bộ lọc
		if isRowMatched(cleanRow, rows[i], filters) {
			
			// Khởi tạo OrderedRow
			rowObj := &OrderedRow{
				RowIndex: i + RANGES.DATA_START_ROW, // Tính index thật trong Excel
				Columns:  make(map[int]string),
			}
			
			rawRow := rows[i]
			
			if fetchAll {
				// Lấy hết từ cột 0 đến 60
				for colIdx := 0; colIdx < maxColLimit; colIdx++ {
					val := ""
					if colIdx < len(rawRow) { val = SafeString(rawRow[colIdx]) }
					rowObj.Columns[colIdx] = val
				}
			} else {
				// Chỉ lấy các cột được yêu cầu
				for _, colIdx := range returnCols {
					val := ""
					if colIdx >= 0 && colIdx < len(rawRow) { val = SafeString(rawRow[colIdx]) }
					rowObj.Columns[colIdx] = val
				}
			}
			
			results[count] = rowObj
			count++
		}
	}
	STATE.SheetMutex.RUnlock() // Mở khóa ĐỌC

	// 6. Trả về kết quả
	if count == 0 {
		json.NewEncoder(w).Encode(SearchResponse{
			Status: "false", Messenger: "Không tìm thấy dữ liệu", Count: 0, Data: make(map[int]*OrderedRow),
		})
	} else {
		// Custom Marshaler sẽ lo việc sắp xếp JSON
		json.NewEncoder(w).Encode(SearchResponse{
			Status: "true", Messenger: "Thành công", Count: count, Data: results,
		})
	}
}
