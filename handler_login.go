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
   - Tự động quản lý trạng thái:
     + Ưu tiên lấy nick đang chạy dở (Resume).
     + Lấy nick cũ đang chờ.
     + Lấy nick mới trong kho.
   - Ghi nhận lịch sử chạy vào cột Note (Bảo toàn số lần chạy).

2. CẤU TRÚC BODY REQUEST CHI TIẾT:
{
  "type": "auto",             // Lệnh: "login", "register", "auto", "auto_reset", "login_reset"
  "token": "...",             // Token xác thực
  "deviceId": "...",          // ID thiết bị (Bắt buộc)
  
  // --- TÙY CHỌN 1: LẤY CHÍNH XÁC (Ưu tiên Tuyệt Đối) ---
  "row_index": 123,           // Lấy chính xác dòng 123 (Index tính từ 0 của Excel)

  // --- TÙY CHỌN 2: BỘ LỌC DỮ LIỆU (Dùng cho Auto/Reg) ---
  // Hỗ trợ lọc theo cột. X là số thứ tự cột (0, 1, 2...).
  "search_and": {             // Điều kiện VÀ (Tất cả phải đúng)
      "match_col_6": ["gmail.com"],       // Cột 6 (Email) CHÍNH XÁC là "gmail.com"
      "contains_col_1": ["vps"],          // Cột 1 (Note) CÓ CHỨA chữ "vps"
      "min_col_29": 1000,                 // Cột 29 (Follower) LỚN HƠN HOẶC BẰNG 1000
      "max_col_25": 5,                    // Cột 25 (Today Post) NHỎ HƠN HOẶC BẰNG 5
      "last_hours_col_28": 24             // Cột 28 (Last Active) trong vòng 24h qua
  },
  "search_or": { ... },       // Điều kiện HOẶC (Chỉ cần 1 điều kiện đúng)

  // --- TÙY CHỌN 3: CẬP NHẬT KHI LẤY ---
  "updated": {
      "col_18": "UserAgent mới...",       // Cập nhật UserAgent ngay khi lấy nick
      "col_19": "192.168.1.1"             // Cập nhật Proxy ngay khi lấy nick
      // ⚠️ LƯU Ý: Không thể cập nhật cột Status(0), Note(1), DeviceId(2) ở đây.
  }
}

3. QUY TRÌNH ƯU TIÊN (PRIORITY FUNNEL):
   - Bước 1: Tìm nick "Đang chạy" (Running) khớp DeviceId.
   - Bước 2: Tìm nick "Đang chờ" (Waiting) khớp DeviceId.
   - Bước 3: Tìm nick "Đăng nhập" (Login) khớp DeviceId.
   - Bước 4: Tìm nick "Đăng nhập" (Login) chưa có chủ (Kho chung).
*/

// Cấu trúc phản hồi chuẩn JSON
type LoginResponse struct {
	Status          string          `json:"status"`
	Type            string          `json:"type"`
	Messenger       string          `json:"messenger"`
	DeviceId        string          `json:"deviceId"`
	RowIndex        int             `json:"row_index"`
	SystemEmail     string          `json:"system_email"`
	AuthProfile     AuthProfile     `json:"auth_profile"`
	ActivityProfile ActivityProfile `json:"activity_profile"`
	AiProfile       AiProfile       `json:"ai_profile"`
}

// Cấu trúc bước ưu tiên
type PriorityStep struct {
	Status  string
	IsMy    bool // True: Phải khớp DeviceId | False: Không cần khớp
	IsEmpty bool // True: Phải chưa có DeviceId | False: Bỏ qua
	PrioID  int  // Mức độ ưu tiên (để ghi log logic)
}

// =================================================================================================
// 🟢 HANDLER CHÍNH
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON Body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi cấu trúc JSON"}`, 400); return
	}

	// 2. Lấy Token Context (Đã qua Middleware)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	// Chuẩn hóa Action
	action := "login"
	if reqType == "register" { action = "register" } else if reqType == "auto" { action = "auto" } else if reqType == "auto_reset" { action = "auto_reset" } else if reqType == "login_reset" { action = "login_reset" }
	
	// Lấy dữ liệu cần update kèm theo (nếu có)
	updateMap := parseUpdateDataLogin(body)

	// 3. Gọi hàm xử lý logic cốt lõi
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action, updateMap)

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

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string, updateMap map[int]interface{}) (*LoginResponse, error) {
	// 1. Tải dữ liệu từ RAM (Không ép tải từ Google để tối ưu tốc độ)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu: %v", err) }

	filters := parseFilterParams(body)
	
	// Khóa ĐỌC (RLock) để quét dữ liệu nhanh
	STATE.SheetMutex.RLock()
	
	// Lấy độ dài mảng hiện tại để kiểm tra biên (Bounds Check)
	rawLen := len(cacheData.RawValues)

	// =========================================================================================
	// 🎯 ƯU TIÊN 1: LẤY THEO ROW INDEX (Chỉ định đích danh)
	// =========================================================================================
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			idx := int(val) - RANGES.DATA_START_ROW
			// Kiểm tra biên an toàn
			if idx >= 0 && idx < rawLen {
				// Check Filter (nếu có)
				if filters.HasFilter {
					if !isRowMatched(cacheData.CleanValues[idx], cacheData.RawValues[idx], filters) {
						STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Dòng %d không khớp bộ lọc", idx)
					}
				}
				// Check Chất lượng
				valQ := KiemTraChatLuongClean(cacheData.CleanValues[idx], action)
				
				// Mở khóa ĐỌC trước khi cam kết
				STATE.SheetMutex.RUnlock()
				
				// Gọi hàm Cam kết (Sẽ tự Khóa GHI bên trong)
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), valQ.SystemEmail, action, 0, updateMap)
			}
			STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Dòng %d không tồn tại", idx)
		}
	}

	// =========================================================================================
	// 🎯 ƯU TIÊN 2: PHỄU LỌC TỰ ĐỘNG (PRIORITY FUNNEL)
	// =========================================================================================
	steps := buildPrioritySteps(action)

	for _, step := range steps {
		// Lấy danh sách index theo Status (O(1) nhờ Map)
		indices := cacheData.StatusMap[step.Status]
		
		for _, idx := range indices {
			// Kiểm tra biên an toàn (Tránh Panic)
			if idx < rawLen {
				row := cacheData.CleanValues[idx]
				isMyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId)
				isEmptyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == "")
				
				// Kiểm tra Logic sở hữu (Của mình hoặc Trống)
				if (step.IsMy && isMyDevice) || (step.IsEmpty && isEmptyDevice) {
					
					// Kiểm tra Bộ lọc (Filter)
					if filters.HasFilter {
						if !isRowMatched(row, cacheData.RawValues[idx], filters) { continue }
					}
					
					// Kiểm tra Chất lượng Nick (Có User/Pass/Email không?)
					val := KiemTraChatLuongClean(row, action)
					if !val.Valid {
						// ⚠️ Nick lỗi -> Tự động sửa (Self-Healing)
						// Phải mở khóa ĐỌC để gọi hàm sửa, sau đó khóa lại
						STATE.SheetMutex.RUnlock()
						doSelfHealing(sid, idx, val.Missing, cacheData)
						STATE.SheetMutex.RLock() // Khóa lại để chạy tiếp vòng lặp
						continue
					}

					// ✅ TÌM THẤY NICK NGON! -> CHUYỂN QUA CHẾ ĐỘ GHI (CRITICAL SECTION)
					STATE.SheetMutex.RUnlock() // 1. Mở khóa đọc
					STATE.SheetMutex.Lock()    // 2. Khóa ghi (Chặn tất cả luồng khác)

					// 🔥 [AN TOÀN] DOUBLE CHECK (Kiểm tra lại lần nữa)
					// Vì giữa lúc Unlock -> Lock, có thể có luồng khác (Cleanup) đã thay đổi dữ liệu
					if idx >= len(cacheData.CleanValues) {
						// Nếu index bị tràn (do xóa dòng) -> Bỏ qua, quay lại tìm tiếp
						STATE.SheetMutex.Unlock(); STATE.SheetMutex.RLock(); continue
					}

					currRow := cacheData.CleanValues[idx] // Lấy dữ liệu mới nhất
					// Kiểm tra lại quyền sở hữu (Tránh bị đứa khác cướp trong tíc tắc)
					if (step.IsMy && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
						
						// 3. Gán chủ quyền NGAY LẬP TỨC trong RAM (Để xí chỗ)
						updateRowCache(cacheData, idx, "", "", deviceId)
						
						STATE.SheetMutex.Unlock() // 4. Mở khóa ghi
						
						// 5. Thực hiện cam kết và trả về
						return commit_and_response(sid, deviceId, cacheData, idx, determineType(currRow), val.SystemEmail, action, step.PrioID, updateMap)
					}
					
					// Nếu check lại thấy không ổn -> Quay lại vòng lặp
					STATE.SheetMutex.Unlock(); STATE.SheetMutex.RLock()
				}
			}
		}
	}
	
	// =========================================================================================
	// 🎯 ƯU TIÊN 3: KIỂM TRA ĐÃ HOÀN THÀNH (COMPLETED CHECK)
	// =========================================================================================
	// Chỉ check nếu là các lệnh Auto/Login
	checkList := []string{"login", "auto", "login_reset", "register"}
	isCheck := false
	for _, s := range checkList { if strings.Contains(action, s) { isCheck = true; break } }
	
	if isCheck {
		// ⚡ TỐI ƯU TỐC ĐỘ O(1): Kiểm tra trực tiếp trong AssignedMap
		if idx, ok := cacheData.AssignedMap[deviceId]; ok {
			// Nếu thiết bị này đang giữ 1 nick, kiểm tra xem nick đó có phải COMPLETED không
			if idx < len(cacheData.CleanValues) && cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] == STATUS_READ.COMPLETED {
				STATE.SheetMutex.RUnlock()
				return nil, fmt.Errorf("Tài khoản hiện tại đã hoàn thành")
			}
		}

		// Fallback: Quét danh sách Completed (Để chắc chắn logic cũ vẫn đúng)
		// Chỉ chạy nếu AssignedMap không tìm thấy (Rất hiếm)
		completedIndices := cacheData.StatusMap[STATUS_READ.COMPLETED]
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
// 🛠️ CÁC HÀM HỖ TRỢ (HELPER FUNCTIONS)
// =================================================================================================

// Xây dựng các bước ưu tiên dựa trên hành động
func buildPrioritySteps(action string) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	if action == "login" || action == "login_reset" {
		add(STATUS_READ.RUNNING, true, false, 1)   // 1. Nick mình đang chạy
		add(STATUS_READ.WAITING, true, false, 2)   // 2. Nick mình đang chờ
		add(STATUS_READ.LOGIN, true, false, 3)     // 3. Nick mình cần login
		add(STATUS_READ.LOGIN, false, true, 4)     // 4. Nick kho chung cần login
		if action == "login_reset" { add(STATUS_READ.COMPLETED, true, false, 5) } // 5. Reset nick xong
	
	} else if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1)
		add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3)
		add(STATUS_READ.REGISTER, false, true, 4)
	
	} else if action == "auto" || action == "auto_reset" {
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

// Hàm Cam Kết (Commit): Ghi dữ liệu chính thức và trả về kết quả
func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string, priority int, updateMap map[int]interface{}) (*LoginResponse, error) {
	row := cache.RawValues[idx]
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }

	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false
	if (strings.Contains(action, "reset")) && (priority == 5 || priority == 99) {
		mode = "reset"; isResetCompleted = true
	}
	tNote := tao_ghi_chu_chuan_login(oldNote, tSt, mode)

	// KHÓA GHI ĐỂ UPDATE DATA
	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	// 1. Dọn dẹp nick cũ (Chuyển về Waiting)
	// Tìm các nick khác mà thiết bị này đang giữ
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		
		cNote := tao_ghi_chu_chuan_login(cOldNote, cSt, "normal")
		if isResetCompleted { cNote = tao_ghi_chu_chuan_login(cOldNote, "Reset chờ chạy", "reset") }
		
		// Update RAM cho nick cũ
		updateRowCache(cache, cIdx, cSt, cNote, "")
		
		// Đẩy nick cũ vào Queue ghi đĩa
		cRow := make([]interface{}, len(cache.RawValues[cIdx])); copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// 2. Update nick mới (Áp dụng các trường updated từ request)
	for colIdx, val := range updateMap {
		if colIdx >= 0 && colIdx < len(cache.RawValues[idx]) {
			// ⛔ BLACKLIST: Không cho phép sửa các cột hệ thống quản lý
			if colIdx == INDEX_DATA_TIKTOK.STATUS || colIdx == INDEX_DATA_TIKTOK.NOTE || colIdx == INDEX_DATA_TIKTOK.DEVICE_ID { continue }
			
			cache.RawValues[idx][colIdx] = val
			if colIdx < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][colIdx] = CleanString(val) }
		}
	}
	// Update trạng thái chính thức vào RAM
	updateRowCache(cache, idx, tSt, tNote, deviceId)

	// 3. Đẩy nick mới vào Queue ghi đĩa
	newRow := make([]interface{}, len(cache.RawValues[idx])); copy(newRow, cache.RawValues[idx])
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	msg := "Lấy nick thành công"
	return &LoginResponse{
		Status: "true", Type: typ, Messenger: msg, DeviceId: deviceId, RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: MakeAuthProfile(newRow), ActivityProfile: MakeActivityProfile(newRow), AiProfile: MakeAiProfile(newRow),
	}, nil
}

// Đồng bộ RAM (Rất quan trọng): Cập nhật Map và List
func updateRowCache(cache *SheetCacheData, idx int, newSt, newNote, newDev string) {
	// Lấy giá trị cũ để so sánh
	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	oldDev := cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]

	// Update Raw Values
	if newSt != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = newSt }
	if newNote != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = newNote }
	if newDev != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = newDev }

	// Update Clean Values
	if newSt != "" && INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(newSt) }
	if newNote != "" && INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(newNote) }
	if newDev != "" && INDEX_DATA_TIKTOK.DEVICE_ID < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(newDev) }

	// 🔥 ĐỒNG BỘ MAP (Sync Maps)
	if newSt != "" {
		newStClean := CleanString(newSt)
		if oldCleanSt != newStClean {
			removeFromStatusMap(cache.StatusMap, oldCleanSt, idx) // Xóa khỏi nhóm cũ
			cache.StatusMap[newStClean] = append(cache.StatusMap[newStClean], idx) // Thêm vào nhóm mới
		}
	}
	if newDev != "" {
		newDevClean := CleanString(newDev)
		if oldDev != newDevClean {
			// Xóa khỏi trạng thái cũ
			if oldDev != "" { delete(cache.AssignedMap, oldDev) } else { removeFromIntList(&cache.UnassignedList, idx) }
			
			// Thêm vào trạng thái mới
			if newDevClean != "" { cache.AssignedMap[newDevClean] = idx } else { cache.UnassignedList = append(cache.UnassignedList, idx) }
		}
	}
}

// Parse dữ liệu cập nhật từ request
func parseUpdateDataLogin(body map[string]interface{}) map[int]interface{} {
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

func determineType(row []string) string {
	st := row[INDEX_DATA_TIKTOK.STATUS]
	if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG { return "register" }
	return "login"
}

func getCleanupIndices(cache *SheetCacheData, deviceId string, targetIdx int, isResetCompleted bool) []int {
	var list []int
	checkList := []string{STATUS_READ.RUNNING, STATUS_READ.REGISTERING}
	if isResetCompleted { checkList = append(checkList, STATUS_READ.COMPLETED) }
	
	// Duyệt qua các trạng thái cần dọn dẹp
	for _, st := range checkList {
		indices := cache.StatusMap[st]
		for _, idx := range indices {
			// Loại trừ chính dòng đang lấy (targetIdx)
			if idx != targetIdx && idx < len(cache.CleanValues) {
				if cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId { list = append(list, idx) }
			}
		}
	}
	return list
}

// Tự sửa lỗi data (Thiếu user/pass)
func doSelfHealing(sid string, idx int, missing string, cache *SheetCacheData) {
	msg := "Nick thiếu " + missing + "\n" + time.Now().Format("02/01/2006 15:04:05")
	STATE.SheetMutex.Lock() // Khóa ghi để sửa
	if idx < len(cache.RawValues) {
		updateRowCache(cache, idx, STATUS_WRITE.ATTENTION, msg, "")
	}
	fullRow := make([]interface{}, len(cache.RawValues[idx])); copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()
	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

// Logic tạo Note LOGIN: Tăng số lần chạy nếu cần
func tao_ghi_chu_chuan_login(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" { return fmt.Sprintf("%s\n%s", newStatus, nowFull) }
	
	oldNote = SafeString(oldNote)
	count := 1
	// Regex bắt số lần chạy: (Lần 5)
	match := REGEX_COUNT.FindStringSubmatch(oldNote)
	if len(match) > 1 { if c, err := strconv.Atoi(match[1]); err == nil { count = c } }

	today := nowFull[:10]; oldDate := ""
	lines := strings.Split(oldNote, "\n")
	for _, l := range lines { 
		if m := REGEX_DATE.FindString(l); m != "" { oldDate = m; break }
	}

	// Logic reset bộ đếm: Nếu khác ngày -> Về 1. Nếu cùng ngày và reset -> Tăng 1.
	if oldDate != today { count = 1 } else { if mode == "reset" { count++ } }

	st := newStatus
	if st == "" && len(lines) > 0 { st = lines[0] } // Giữ status cũ trong note nếu không có mới
	if st == "" { st = "Đang chạy" }
	
	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
