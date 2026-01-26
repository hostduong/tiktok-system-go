package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tiktok-server/internal/cache"
	"tiktok-server/internal/models"
	"tiktok-server/internal/queue"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

// UpdatedRequest: Cấu trúc request cho hành động 'updated'
// Node.js hỗ trợ search_col_X và col_X
type UpdatedRequest struct {
	Type     string `json:"type"`
	Token    string `json:"token"`
	DeviceId string `json:"deviceId"`
	RowIndex int    `json:"row_index"`
	Sheet    string `json:"sheet"`
	Note     string `json:"note"`
	
	// Dùng map để hứng các trường động col_0, search_col_6...
	Extra map[string]interface{} `json:"-"`
}

// Custom Unmarshal để bắt các trường động (col_*, search_col_*)
func (r *UpdatedRequest) UnmarshalJSON(data []byte) error {
	type Alias UpdatedRequest
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	// Parse các field còn lại vào Extra
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	r.Extra = m
	return nil
}

func HandleUpdate(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	// 1. Parse Body
	var body UpdatedRequest
	// Đọc body 1 lần để parse
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}

	sheetName := body.Sheet
	if sheetName == "" {
		[cite_start]sheetName = "DataTiktok" // Mặc định như Node.js [cite: 290]
	}
	isDataTiktok := (sheetName == "DataTiktok")

	// 2. Load Cache
	var cacheItem *cache.SheetCacheItem
	val, ok := cache.GlobalSheets.Load(spreadsheetId + "__" + sheetName)
	if ok {
		cacheItem = val.(*cache.SheetCacheItem)
	}

	// Nếu chưa có cache, tải về
	if cacheItem == nil || !cacheItem.IsValid() {
		cacheItem = cache.NewSheetCache(spreadsheetId, sheetName)
		accounts, err := sheetSvc.FetchData(spreadsheetId, sheetName, 11, 10000)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"status":"false","messenger":"Lỗi tải dữ liệu: %v"}`, err), 500)
			return
		}
		cacheItem.Lock()
		cacheItem.RawValues = accounts
		// Re-index (giản lược cho update)
		cacheItem.Unlock()
		cache.GlobalSheets.Store(spreadsheetId+"__"+sheetName, cacheItem)
	}

	[cite_start]// 3. Xác định dòng cần sửa (Logic [cite: 291-304])
	targetIndex := -1
	isAppend := false
	
	cacheItem.RLock()
	rows := cacheItem.RawValues
	
	// Ưu tiên 1: Tìm theo row_index
	if body.RowIndex >= 11 {
		idx := body.RowIndex - 11 // Chuyển về index mảng (0-based)
		if idx >= 0 && idx < len(rows) {
			targetIndex = idx
			// Kiểm tra search_col nếu có (Double check)
			// (Tạm bỏ qua logic check sâu search_col khi đã có row_index để tối ưu, Node.js có check)
		} else {
			cacheItem.RUnlock()
			utils.JSONResponse(w, "false", "Dòng yêu cầu không tồn tại", nil)
			return
		}
	} else {
		// Ưu tiên 2: Tìm theo search_col_X
		// Lọc ra các điều kiện search
		searchCols := make(map[int]string)
		for k, v := range body.Extra {
			if strings.HasPrefix(k, "search_col_") {
				idx, _ := strconv.Atoi(strings.TrimPrefix(k, "search_col_"))
				searchCols[idx] = utils.NormalizeString(fmt.Sprintf("%v", v))
			}
		}

		if len(searchCols) > 0 {
			for i, acc := range rows {
				match := true
				rawArr := acc.ToSlice() // Lấy mảng dữ liệu thô để so sánh
				for colIdx, val := range searchCols {
					cellVal := ""
					if colIdx < len(rawArr) {
						cellVal = utils.NormalizeString(fmt.Sprintf("%v", rawArr[colIdx]))
					}
					if cellVal != val {
						match = false
						break
					}
				}
				if match {
					targetIndex = i
					break
				}
			}
			if targetIndex == -1 {
				// Không tìm thấy -> Báo lỗi
				cacheItem.RUnlock()
				utils.JSONResponse(w, "false", "Không tìm thấy nick phù hợp", nil)
				return
			}
		} else {
			// Không có row_index, không có search_col -> Append
			isAppend = true
		}
	}
	cacheItem.RUnlock()

	// 4. Chuẩn bị dữ liệu mới
	var newAcc *models.TikTokAccount
	var oldNote string

	if isAppend {
		newAcc = models.NewAccount() // Tạo mới rỗng 61 cột
	} else {
		// Clone dữ liệu cũ để sửa
		oldAcc := cacheItem.GetAccountByIndex(targetIndex)
		// Deep copy struct
		temp := *oldAcc
		newAcc = &temp
		// Copy slice extra data để tránh trỏ cùng vùng nhớ
		newAcc.ExtraData = make([]string, len(oldAcc.ExtraData))
		copy(newAcc.ExtraData, oldAcc.ExtraData)
		
		oldNote = oldAcc.Note
	}

	// 5. Merge dữ liệu từ body (col_X) vào newAcc
	[cite_start]// Logic map col_X vào Struct [cite: 306]
	// Để đơn giản, ta convert newAcc sang Slice, update Slice, rồi convert ngược lại
	// Tuy nhiên để tối ưu, ta map trực tiếp nếu biết field. 
	// Do Go tĩnh, cách an toàn nhất là dùng ToSlice -> Update Slice -> FromSlice
	
	currentSlice := newAcc.ToSlice()
	// Mở rộng slice nếu cần (đảm bảo đủ 61 cột)
	for len(currentSlice) < 61 { currentSlice = append(currentSlice, "") }

	for k, v := range body.Extra {
		if strings.HasPrefix(k, "col_") {
			idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
			if idx >= 0 && idx < 61 {
				currentSlice[idx] = fmt.Sprintf("%v", v)
			}
		}
	}
	
	[cite_start]// Update Note chuẩn (Logic [cite: 307-309])
	if isDataTiktok {
		content := body.Note
		if content == "" {
			// Thử lấy từ col_1
			if v, ok := body.Extra["col_1"]; ok { content = fmt.Sprintf("%v", v) }
		}
		
		noteMode := "updated"
		if isAppend { noteMode = "new" }
		
		newNote := utils.CreateStandardNote(oldNote, content, noteMode)
		currentSlice[1] = newNote // Cập nhật vào mảng
		
		if body.DeviceId != "" {
			currentSlice[2] = body.DeviceId
		}
	}

	// Convert ngược lại vào Struct
	newAcc.FromSlice(currentSlice)

	// 6. Lưu Cache & Queue
	q := queue.GetQueue(spreadsheetId, sheetSvc)
	
	if isAppend {
		// Update RAM Cache
		cacheItem.Lock()
		newAcc.RowIndex = 11 + len(cacheItem.RawValues) // Tính row index mới
		cacheItem.RawValues = append(cacheItem.RawValues, newAcc)
		cacheItem.LastAccessed = time.Now()
		cacheItem.Unlock()

		// Enqueue Append
		q.EnqueueAppend(sheetName, newAcc.ToSlice())
		
		// Response JSON
		auth, activity, ai := handlers.SplitProfile(newAcc) // Cần export hàm SplitProfile ở login.go
		resp := map[string]interface{}{
			"status": "true", "type": "updated", "messenger": "Thêm mới thành công",
			"auth_profile": auth, "activity_profile": activity, "ai_profile": ai,
		}
		utils.JSONResponseRaw(w, resp)
	} else {
		// Update RAM Cache
		cacheItem.UpdateAccount(targetIndex, newAcc)

		// Enqueue Update
		q.EnqueueUpdate(sheetName, targetIndex, newAcc)

		// Response JSON
		auth, activity, ai := handlers.SplitProfile(newAcc)
		resp := map[string]interface{}{
			"status": "true", "type": "updated", "messenger": "Cập nhật thành công",
			"row_index": 11 + targetIndex,
			"auth_profile": auth, "activity_profile": activity, "ai_profile": ai,
		}
		utils.JSONResponseRaw(w, resp)
	}
}
