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
   - Tìm kiếm dữ liệu và trả về kết quả ĐƯỢC SẮP XẾP CHUẨN (row_index đầu tiên, col theo số tự nhiên).
   - Khắc phục lỗi hiển thị col_10 đứng trước col_2 của JSON mặc định.

2. CẤU TRÚC BODY REQUEST:
{
  "token": "...",
  "sheet": "DataTiktok",
  "limit": 50,
  "return_cols": [],          // Rỗng = Lấy đủ 61 cột.

  "search_and": { ... },
  "search_or": { ... }
}

3. CẤU TRÚC RESPONSE (Đảm bảo thứ tự tuyệt đối):
{
    "status": "true",
    "messenger": "Thành công",
    "count": 1,
    "data": {
        "0": {
            "row_index": 15,     <-- LUÔN ĐỨNG ĐẦU
            "col_0": "...",
            "col_1": "...",
            "col_2": "...",      <-- LUÔN ĐỨNG TRƯỚC col_10
            ...
            "col_10": "...",
            ...
            "col_60": ""
        }
    }
}
*/

// =================================================================================================
// 🟢 STRUCT TÙY BIẾN ĐỂ SẮP XẾP JSON (CUSTOM MARSHALER)
// =================================================================================================

// OrderedRow: Struct đại diện cho 1 dòng, dùng để Custom JSON
type OrderedRow struct {
	RowIndex int            // Số dòng
	Columns  map[int]string // Dữ liệu các cột (Key là số int để dễ duyệt)
}

// MarshalJSON: Hàm này sẽ được gọi tự động khi json.Encode.
// Tại đây chúng ta TỰ TAY viết chuỗi JSON để ép nó theo thứ tự mong muốn.
func (r *OrderedRow) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	
	// 1. Mở ngoặc nhọn và ghi row_index đầu tiên
	buf.WriteString(fmt.Sprintf(`{"row_index":%d`, r.RowIndex))

	// 2. Duyệt vòng lặp từ 0 đến 60 (Theo đúng thứ tự số học)
	// Chỉ ghi những cột ĐANG CÓ trong map Columns
	maxLimit := CACHE.CLEAN_COL_LIMIT // 61
	for i := 0; i < maxLimit; i++ {
		if val, exists := r.Columns[i]; exists {
			// Chuẩn bị key và value
			// Marshal value để đảm bảo các ký tự đặc biệt trong chuỗi được escape đúng chuẩn JSON
			valJson, _ := json.Marshal(val)
			
			// Ghi vào buffer: ,"col_X":"giá trị"
			buf.WriteString(fmt.Sprintf(`,"col_%d":%s`, i, valJson))
		}
	}

	// 3. Đóng ngoặc nhọn
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
	Data      map[int]*OrderedRow   `json:"data"` // Dùng con trỏ đến OrderedRow
}

// =================================================================================================
// 🟢 HANDLER CHÍNH
// =================================================================================================

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

	// 4. Phân tích tham số
	filters := parseFilterParams(body)
	
	limit := 1000
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
	// Sort để đảm bảo tính nhất quán (dù Custom Marshaler đã lo việc hiển thị)
	sort.Ints(returnCols)
	fetchAll := (len(returnCols) == 0)

	// 5. Thực hiện tìm kiếm
	results := make(map[int]*OrderedRow) // Map kết quả chứa OrderedRow
	count := 0
	
	STATE.SheetMutex.RLock()
	rows := cacheData.RawValues
	cleanRows := cacheData.CleanValues
	
	maxColLimit := CACHE.CLEAN_COL_LIMIT // 61

	for i, cleanRow := range cleanRows {
		if count >= limit { break }

		if isRowMatched(cleanRow, rows[i], filters) {
			
			// Khởi tạo OrderedRow cho dòng này
			rowObj := &OrderedRow{
				RowIndex: i + RANGES.DATA_START_ROW,
				Columns:  make(map[int]string),
			}
			
			rawRow := rows[i]
			
			if fetchAll {
				// Lấy hết 0->60
				for colIdx := 0; colIdx < maxColLimit; colIdx++ {
					val := ""
					if colIdx < len(rawRow) { val = SafeString(rawRow[colIdx]) }
					rowObj.Columns[colIdx] = val
				}
			} else {
				// Lấy theo cột yêu cầu
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
	STATE.SheetMutex.RUnlock()

	// 6. Trả về kết quả
	if count == 0 {
		json.NewEncoder(w).Encode(SearchResponse{
			Status: "false", Messenger: "Không tìm thấy dữ liệu", Count: 0, Data: make(map[int]*OrderedRow),
		})
	} else {
		// Lúc này, khi Encode, hàm MarshalJSON của OrderedRow sẽ được gọi
		// -> Tạo ra chuỗi JSON đẹp chuẩn từng milimet.
		json.NewEncoder(w).Encode(SearchResponse{
			Status: "true", Messenger: "Thành công", Count: count, Data: results,
		})
	}
}
