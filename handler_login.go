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
📘 TÀI LIỆU API: LẤY TÀI KHOẢN (POST /tool/account)
=================================================================================================

1. MỤC ĐÍCH:
   - Phân phối tài khoản cho tool chạy (Login, Reg, Auto).
   - Tự động quản lý trạng thái, chuyển nick cũ về chờ, lấy nick mới.
   - Ghi nhận lịch sử chạy vào cột Note (Ghi chú).

2. CẤU TRÚC BODY REQUEST:
{
  "type": "auto",             // Lệnh: "login", "register", "auto", "auto_reset", "login_reset"
  "token": "...",             // Token xác thực
  "deviceId": "...",          // ID thiết bị
  
  // --- TÙY CHỌN 1: LẤY CHÍNH XÁC (Ưu tiên cao nhất) ---
  "row_index": 123,           // Lấy chính xác dòng 123 (nếu thỏa mãn điều kiện)

  // --- TÙY CHỌN 2: BỘ LỌC DỮ LIỆU (Kết hợp với Logic ưu tiên) ---
  "search_and": {             // Điều kiện VÀ (Tất cả phải đúng)
      "match_col_6": ["gmail.com"],   // Cột 6 phải là gmail
      "min_col_29": 1000              // Cột 29 >= 1000
  },
  "search_or": { ... },       // Điều kiện HOẶC (1 trong các điều kiện đúng)

  // --- TÙY CHỌN 3: CẬP NHẬT KHI LẤY ---
  "updated": {
      "col_18": "UserAgent mới" // Cập nhật ngay dữ liệu này khi lấy nick
  }
}

3. QUY TRÌNH ƯU TIÊN (PRIORITY FUNNEL):
   - Bước 1: Tìm nick "Đang chạy" (Running) của Device này.
   - Bước 2: Tìm nick "Đang chờ" (Waiting) của Device này.
   - Bước 3: Tìm nick "Đăng nhập" (Login) -> Ưu tiên của mình -> Sau đó đến kho chung (Trống DeviceId).
   - Bước 4: (Nếu là Auto/Reg) Tìm nick "Đang/Chờ/Đăng ký".
*/

// =================================================================================================
// 🟢 CẤU TRÚC PHẢN HỒI (RESPONSE)
// =================================================================================================

type LoginResponse struct {
	Status          string          `json:"status"`          // "true" / "false"
	Type            string          `json:"type"`            // Loại lệnh
	Messenger       string          `json:"messenger"`       // Thông báo kết quả
	DeviceId        string          `json:"deviceId"`        // Thiết bị nhận
	RowIndex        int             `json:"row_index"`       // Chỉ số dòng trong Excel
	SystemEmail     string          `json:"system_email"`    // Email hệ thống tách từ cột Email
	AuthProfile     AuthProfile     `json:"auth_profile"`    // Thông tin đăng nhập
	ActivityProfile ActivityProfile `json:"activity_profile"`// Thông tin hoạt động
	AiProfile       AiProfile       `json:"ai_profile"`      // Cấu hình AI
}

// Cấu trúc định nghĩa các bước ưu tiên
type PriorityStep struct {
	Status  string // Trạng thái cần tìm (VD: "đang chạy")
	IsMy    bool   // True: Chỉ tìm nick đã gán cho Device này
	IsEmpty bool   // True: Chỉ tìm nick chưa gán cho ai (Kho chung)
	PrioID  int    // Mã ưu tiên (Dùng để debug nếu cần)
}

// =================================================================================================
// 🟢 HANDLER CHÍNH (Tiếp nhận Request)
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON từ Body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi định dạng JSON"}`, 400)
		return
	}

	// 2. Lấy thông tin Token từ Context (Middleware đã xử lý)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	// 3. Chuẩn hóa dữ liệu đầu vào
	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	// Chuẩn hóa action (login/register/auto)
	action := "login"
	if reqType == "register" { action = "register" } else if reqType == "auto" { action = "auto" } else if reqType == "auto_reset" { action = "auto_reset" } else if reqType == "login_reset" { action = "login_reset" }
	
	// Lấy dữ liệu update kèm theo (nếu có)
	updateMap := parseUpdateDataLogin(body)

	// 4. Gọi hàm xử lý logic chính
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action, updateMap)

	// 5. Trả về kết quả JSON
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		// Trả về lỗi chi tiết để Client biết (VD: "Các tài khoản đã hoàn thành")
		json.NewEncoder(w).Encode(map[string]string{
			"status":    "false",
			"messenger": err.Error(),
		})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC LÕI (CORE LOGIC)
// =================================================================================================

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string, updateMap map[int]interface{}) (*LoginResponse, error) {
	// BƯỚC 1: Tải dữ liệu từ Cache (Rất nhanh, hạn chế đọc API Google)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu hệ thống") }

	// BƯỚC 2: Chuẩn bị bộ lọc và khóa đọc (Read Lock)
	filters := parseFilterParams(body)
	STATE.SheetMutex.RLock() // Dùng RLock để nhiều người có thể đọc cùng lúc
	rawLen := len(cacheData.RawValues)

	// --- CHIẾN LƯỢC 1: LẤY THEO ROW_INDEX (Ưu tiên tuyệt đối) ---
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			idx := int(val) - RANGES.DATA_START_ROW
			if idx >= 0 && idx < rawLen {
				// Kiểm tra bộ lọc (nếu Client có gửi kèm)
				if filters.HasFilter {
					if !isRowMatched(cacheData.CleanValues[idx], cacheData.RawValues[idx], filters) {
						STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Dòng yêu cầu không khớp điều kiện lọc")
					}
				}
				// Kiểm tra chất lượng (User/Pass/Email có đủ không?)
				valQ := KiemTraChatLuongClean(cacheData.CleanValues[idx], action)
				
				// Mở khóa đọc để tiến hành ghi
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), valQ.SystemEmail, action, 0, updateMap)
			}
			STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Dòng yêu cầu không tồn tại")
		}
	}

	// --- CHIẾN LƯỢC 2: LẤY THEO QUY TRÌNH ƯU TIÊN (Cái Phễu) ---
	steps := buildPrioritySteps(action)

	for _, step := range steps {
		// Lấy danh sách các dòng thuộc trạng thái cần tìm (VD: "đang chạy")
		indices := cacheData.StatusMap[step.Status]

		for _, idx := range indices {
			if idx < rawLen {
				row := cacheData.CleanValues[idx]
				
				// Kiểm tra quyền sở hữu thiết bị
				isMyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId)
				isEmptyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == "")
				
				// Logic khớp: Của mình (My) hoặc Kho chung (Empty) tùy theo bước
				if (step.IsMy && isMyDevice) || (step.IsEmpty && isEmptyDevice) {
					
					// Kiểm tra bộ lọc nội dung (Search And/Or)
					if filters.HasFilter {
						if !isRowMatched(row, cacheData.RawValues[idx], filters) { continue }
					}
					
					// Kiểm tra chất lượng nick
					val := KiemTraChatLuongClean(row, action)
					if !val.Valid {
						// Tự động sửa lỗi (Self-Healing): Đánh dấu "Chú ý" để không lặp lại
						STATE.SheetMutex.RUnlock(); doSelfHealing(sid, idx, val.Missing, cacheData); STATE.SheetMutex.RLock()
						continue
					}

					// ---> TÌM THẤY NICK HỢP LỆ! <---
					
					// Chuyển sang chế độ Ghi (Write Lock)
					STATE.SheetMutex.RUnlock(); STATE.SheetMutex.Lock()
					
					// Kiểm tra lại lần cuối (Double Check) để tránh tranh chấp
					currRow := cacheData.CleanValues[idx]
					if (step.IsMy && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
						// Gán thiết bị ngay lập tức vào RAM
						updateRowCache(cacheData, idx, "", "", deviceId) // Chỉ cập nhật DeviceId trước
						
						STATE.SheetMutex.Unlock()
						// Chốt giao dịch và trả về kết quả
						return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID, updateMap)
					}
					// Nếu bị tranh chấp, quay lại chế độ Đọc để tìm tiếp
					STATE.SheetMutex.Unlock(); STATE.SheetMutex.RLock()
				}
			}
		}
	}
	
	// --- CHIẾN LƯỢC 3: KIỂM TRA ĐÃ HOÀN THÀNH HẾT CHƯA? ---
	// Nếu không tìm thấy nick nào chạy được, kiểm tra xem có nick "Hoàn thành" không
	checkList := []string{"login", "auto", "login_reset", "register"}
	isCheck := false
	for _, s := range checkList { if strings.Contains(action, s) { isCheck = true; break } }
	
	if isCheck {
		completedIndices := cacheData.StatusMap[STATUS_READ.COMPLETED] // Status: "hoàn thành"
		for _, idx := range completedIndices {
			if idx < rawLen && cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
				STATE.SheetMutex.RUnlock()
				return nil, fmt.Errorf("Các tài khoản đã hoàn thành")
			}
		}
	}

	STATE.SheetMutex.RUnlock()
	return nil, fmt.Errorf("Không còn tài khoản phù hợp")
}

// =================================================================================================
// 🛠 CÁC HÀM HỖ TRỢ (HELPER FUNCTIONS)
// =================================================================================================

// Xây dựng danh sách ưu tiên tìm kiếm
func buildPrioritySteps(action string) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	// Logic Login: Chạy dở -> Chờ -> Kho
	if action == "login" || action == "login_reset" {
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3) // Nick của tôi
		add(STATUS_READ.LOGIN, false, true, 4) // Nick kho chung
		if action == "login_reset" { add(STATUS_READ.COMPLETED, true, false, 5) }
	} else 
	// Logic Register
	if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1)
		add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3)
		add(STATUS_READ.REGISTER, false, true, 4)
	} else 
	// Logic Auto: Login trước -> Register sau
	if action == "auto" || action == "auto_reset" {
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		if action == "auto_reset" { add(STATUS_READ.COMPLETED, true, false, 99) }
		
		add(STATUS_READ.REGISTERING, true, false, 5)
		add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7)
		add(STATUS_READ.REGISTER, false, true, 8)
	}
	return steps
}

// Chốt giao dịch: Cập nhật RAM, Ghi Log, Đẩy Queue
func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string, priority int, updateMap map[int]interface{}) (*LoginResponse, error) {
	row := cache.RawValues[idx]
	
	// Xác định trạng thái mới (Đang chạy / Đang đăng ký)
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }

	// Tạo Note mới (Dùng Regex để đếm số lần chính xác)
	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false
	// Nếu là lệnh reset và lấy nick hoàn thành -> Chế độ Reset
	if (strings.Contains(action, "reset")) && (priority == 5 || priority == 99) {
		mode = "reset"; isResetCompleted = true
	}
	tNote := tao_ghi_chu_chuan_login(oldNote, tSt, mode)

	// Khóa Ghi để cập nhật dữ liệu hàng loạt
	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	// 1. Dọn dẹp: Chuyển các nick cũ "Đang chạy" của Device này về "Đang chờ"
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		cNote := tao_ghi_chu_chuan_login(cOldNote, cSt, "normal")
		if isResetCompleted { cNote = tao_ghi_chu_chuan_login(cOldNote, "Reset chờ chạy", "reset") }
		
		// Cập nhật RAM cho nick cũ
		updateRowCache(cache, cIdx, cSt, cNote, "")
		
		// Đẩy vào hàng đợi ghi đĩa
		cRow := make([]interface{}, len(cache.RawValues[cIdx])); copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// 2. Cập nhật Nick mới lấy (Target Row)
	// Áp dụng các cột update tùy chọn (nếu có trong request)
	for colIdx, val := range updateMap {
		if colIdx >= 0 && colIdx < len(cache.RawValues[idx]) {
			// Bảo vệ các cột hệ thống, không cho client ghi đè bừa bãi
			if colIdx == INDEX_DATA_TIKTOK.STATUS || colIdx == INDEX_DATA_TIKTOK.NOTE || colIdx == INDEX_DATA_TIKTOK.DEVICE_ID { continue }
			cache.RawValues[idx][colIdx] = val
			if colIdx < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][colIdx] = CleanString(val) }
		}
	}
	
	// Cập nhật Status, Note, DeviceID vào RAM
	updateRowCache(cache, idx, tSt, tNote, deviceId)

	// Tạo bản sao dữ liệu để trả về Client và Ghi đĩa
	newRow := make([]interface{}, len(cache.RawValues[idx])); copy(newRow, cache.RawValues[idx])
	
	// Đẩy vào hàng đợi ghi đĩa
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	// Trả về kết quả thành công
	return &LoginResponse{
		Status: "true", Type: typ, Messenger: "Lấy nick thành công", 
		DeviceId: deviceId, RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: MakeAuthProfile(newRow), ActivityProfile: MakeActivityProfile(newRow), AiProfile: MakeAiProfile(newRow),
	}, nil
}

// 🔥 HÀM QUAN TRỌNG: Đồng bộ RAM (StatusMap, AssignedMap)
func updateRowCache(cache *SheetCacheData, idx int, newSt, newNote, newDev string) {
	// Lấy dữ liệu cũ để so sánh
	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	oldDev := cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]

	// 1. Cập nhật Raw Values (Dữ liệu gốc)
	if newSt != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = newSt }
	if newNote != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = newNote }
	if newDev != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = newDev }

	// 2. Cập nhật Clean Values (Dữ liệu tìm kiếm)
	if newSt != "" && INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { 
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(newSt) 
	}
	if newNote != "" && INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { 
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(newNote) 
	}
	if newDev != "" && INDEX_DATA_TIKTOK.DEVICE_ID < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(newDev)
	}

	// 3. Đồng bộ StatusMap (Nếu đổi trạng thái -> chuyển nhóm Index)
	if newSt != "" {
		newStClean := CleanString(newSt)
		if oldCleanSt != newStClean {
			removeFromStatusMap(cache.StatusMap, oldCleanSt, idx)
			cache.StatusMap[newStClean] = append(cache.StatusMap[newStClean], idx)
		}
	}

	// 4. Đồng bộ AssignedMap (Nếu đổi thiết bị -> chuyển quyền sở hữu)
	if newDev != "" {
		newDevClean := CleanString(newDev)
		if oldDev != newDevClean {
			// Xóa khỏi chủ cũ (hoặc kho trống)
			if oldDev != "" {
				delete(cache.AssignedMap, oldDev)
			} else {
				removeFromIntList(&cache.UnassignedList, idx)
			}
			// Gán cho chủ mới
			cache.AssignedMap[newDevClean] = idx
		}
	}
}

// Helper: Xóa phần tử khỏi mảng int (Dùng cho UnassignedList)
func removeFromIntList(list *[]int, target int) {
	for i, v := range *list {
		if v == target {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return
		}
	}
}

// Phân tích dữ liệu update bổ sung (chỉ nhận col_X)
func parseUpdateDataLogin(body map[string]interface{}) map[int]interface{} {
	cols := make(map[int]interface{})
	if v, ok := body["updated"]; ok {
		if updatedMap, ok := v.(map[string]interface{}); ok {
			for k, val := range updatedMap {
				if strings.HasPrefix(k, "col_") {
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

func determineType(row []string) string {
	st := row[INDEX_DATA_TIKTOK.STATUS]
	if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG { return "register" }
	return "login"
}

// Tìm các dòng cần dọn dẹp (Running -> Waiting)
func getCleanupIndices(cache *SheetCacheData, deviceId string, targetIdx int, isResetCompleted bool) []int {
	var list []int
	checkList := []string{STATUS_READ.RUNNING, STATUS_READ.REGISTERING}
	if isResetCompleted { checkList = append(checkList, STATUS_READ.COMPLETED) }
	
	for _, st := range checkList {
		indices := cache.StatusMap[st]
		for _, idx := range indices {
			if idx != targetIdx && idx < len(cache.CleanValues) {
				if cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId { 
					list = append(list, idx) 
				}
			}
		}
	}
	return list
}

// Tự động sửa lỗi (Self-Healing) khi nick thiếu thông tin
func doSelfHealing(sid string, idx int, missing string, cache *SheetCacheData) {
	msg := "Nick thiếu " + missing + "\n" + time.Now().Format("02/01/2006 15:04:05")
	STATE.SheetMutex.Lock()
	if idx < len(cache.RawValues) {
		updateRowCache(cache, idx, STATUS_WRITE.ATTENTION, msg, "")
	}
	fullRow := make([]interface{}, len(cache.RawValues[idx])); copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()
	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

// Logic tạo Note dùng Regex (Giữ nguyên số lần chạy)
func tao_ghi_chu_chuan_login(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" { return fmt.Sprintf("%s\n%s", newStatus, nowFull) }
	
	oldNote = SafeString(oldNote)
	count := 0
	
	// 1. Dùng Regex để bắt số lần cũ chính xác
	match := REGEX_COUNT.FindStringSubmatch(oldNote)
	if len(match) > 1 {
		if c, err := strconv.Atoi(match[1]); err == nil {
			count = c
		}
	}
	if count == 0 { count = 1 }

	// 2. Logic Reset theo ngày
	today := nowFull[:10]
	oldDate := ""
	lines := strings.Split(oldNote, "\n")
	for _, l := range lines { 
		matchDate := REGEX_DATE.FindString(l) 
		if matchDate != "" { oldDate = matchDate; break }
	}

	if oldDate != today { 
		count = 1 // Qua ngày mới -> Reset về 1
	} else { 
		if mode == "reset" { 
			count++ // Reset cùng ngày -> Tăng số lần
		} 
		// Login thường -> Giữ nguyên count
	}

	st := newStatus
	if st == "" && len(lines) > 0 { st = lines[0] }
	if st == "" { st = "Đang chạy" }
	
	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
