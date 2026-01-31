package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// =================================================================================================
// 🟢 CẤU TRÚC RESPONSE
// =================================================================================================

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

type PriorityStep struct {
	Status  string
	IsMy    bool // Nick đã gán cho deviceId này
	IsEmpty bool // Nick chưa ai nhận
	PrioID  int
}

// =================================================================================================
// 🟢 HANDLER CHÍNH
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// 1. Decode Body Request
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}

	// 2. Lấy Token từ Context (Do Middleware xử lý)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	// 3. Chuẩn hóa Action từ Type đầu vào
	// Hỗ trợ: login, login_reset, register, auto, auto_reset
	action := "login"
	if reqType == "register" { action = "register" } else if reqType == "auto" { action = "auto" } else if reqType == "auto_reset" { action = "auto_reset" } else if reqType == "login_reset" { action = "login_reset" }
	
	// 4. Gọi hàm xử lý chính
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)

	// 5. Trả về kết quả
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 CORE LOGIC: XỬ LÝ LẤY DỮ LIỆU
// =================================================================================================

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	// 1. Tải dữ liệu từ Cache (hoặc Google Sheet)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	// 2. Parse Bộ lọc (search_and / search_or) từ root body
	filters := parseFilterParams(body)

	STATE.SheetMutex.RLock() // Khóa đọc để an toàn
	rawLen := len(cacheData.RawValues)

	// =============================================================================================
	// 📍 CHIẾN LƯỢC 1: ROW INDEX (ƯU TIÊN TUYỆT ĐỐI)
	// =============================================================================================
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			idx := int(val) - RANGES.DATA_START_ROW
			
			// Kiểm tra dòng có tồn tại không
			if idx >= 0 && idx < rawLen {
				// Nếu có Filter -> Bắt buộc dòng này phải KHỚP filter mới lấy
				if filters.HasFilter {
					if !isRowMatched(cacheData.CleanValues[idx], cacheData.RawValues[idx], filters) {
						STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("row_index không khớp điều kiện tìm kiếm")
					}
				}
				
				// Nếu không có filter hoặc đã khớp -> LẤY LUÔN (Bỏ qua check status, quality vì user chỉ định)
				// Chỉ check quality nhẹ để lấy System Email trả về
				valQ := KiemTraChatLuongClean(cacheData.CleanValues[idx], action)
				
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), valQ.SystemEmail, action, 0)
			}
			STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("row_index không tồn tại")
		}
	}

	// =============================================================================================
	// 📍 CHIẾN LƯỢC 2: TỰ ĐỘNG THEO QUY TRÌNH (PRIORITY STEPS)
	// =============================================================================================
	// Logic: Duyệt từng bước ưu tiên -> Lọc theo Status -> (Lọc Search nếu có) -> Lấy
	
	// Xây dựng danh sách các bước cần tìm kiếm dựa trên action
	steps := buildPrioritySteps(action)

	for _, step := range steps {
		// Lấy danh sách index các dòng có Status tương ứng (Tra từ Map O(1))
		indices := cacheData.StatusMap[step.Status]
		
		for _, idx := range indices {
			if idx < rawLen {
				row := cacheData.CleanValues[idx]
				
				// 1. Check Device (Nick của mình hoặc Nick trống)
				isMyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId)
				isEmptyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == "")
				
				if (step.IsMy && isMyDevice) || (step.IsEmpty && isEmptyDevice) {
					
					// 2. Check Search (Nếu có search_and / search_or)
					// Nếu có filter -> Gọi hàm kiểm tra. Nếu không khớp -> Bỏ qua nick này
					if filters.HasFilter {
						if !isRowMatched(row, cacheData.RawValues[idx], filters) { continue }
					}
					
					// 3. Check Quality (User/Pass/Email có đủ không)
					val := KiemTraChatLuongClean(row, action)
					if !val.Valid {
						// Nếu nick lỗi -> Tự sửa (Self Healing) ghi chú vào sheet
						STATE.SheetMutex.RUnlock(); doSelfHealing(sid, idx, val.Missing, cacheData); STATE.SheetMutex.RLock()
						continue
					}

					// -> THỎA MÃN TẤT CẢ ĐIỀU KIỆN -> TIẾN HÀNH LẤY NICK
					STATE.SheetMutex.RUnlock(); STATE.SheetMutex.Lock() // Chuyển sang khóa GHI
					
					// Double Check (Kiểm tra lại lần nữa khi đã lock ghi để tránh xung đột luồng)
					currRow := cacheData.CleanValues[idx]
					if (step.IsMy && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
						// Cập nhật người sở hữu mới
						cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
						cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
						cacheData.AssignedMap[deviceId] = idx
						STATE.SheetMutex.Unlock()
						
						// Commit xuống Sheet và trả về
						return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID)
					}
					STATE.SheetMutex.Unlock(); STATE.SheetMutex.RLock() // Nếu tạch double check -> Quay lại khóa đọc
				}
			}
		}
	}
	
	// Check Completed: Nếu không tìm thấy nick nào chạy được, kiểm tra xem có nick nào đã hoàn thành không
	// Mục đích: Để báo lỗi "Hoàn thành" thay vì "Hết tài khoản" cho User biết
	checkList := []string{"login", "auto", "login_reset", "register"}
	isCheck := false
	for _, s := range checkList { if strings.Contains(action, s) { isCheck = true; break } }
	if isCheck {
		completedIndices := cacheData.StatusMap[STATUS_READ.COMPLETED]
		for _, idx := range completedIndices {
			if idx < rawLen && cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
				STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Các tài khoản đã hoàn thành")
			}
		}
	}

	STATE.SheetMutex.RUnlock()
	return nil, fmt.Errorf("Không còn tài khoản phù hợp")
}

// =================================================================================================
// 🛠 CÁC HÀM HỖ TRỢ LOGIC
// =================================================================================================

// buildPrioritySteps: Xây dựng thứ tự ưu tiên tìm kiếm
func buildPrioritySteps(action string) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	// Hàm helper thêm bước vào danh sách
	// st: Status cần tìm, my: Tìm nick của mình, empty: Tìm nick trống, prio: Độ ưu tiên
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	// 1. Nhóm Login / Login Reset
	// Thứ tự: Đang chạy -> Đang chờ -> Đăng nhập
	if action == "login" || action == "login_reset" {
		add(STATUS_READ.RUNNING, true, false, 1) // Ưu tiên 1: Đang chạy (Của mình)
		add(STATUS_READ.WAITING, true, false, 2) // Ưu tiên 2: Đang chờ (Của mình)
		add(STATUS_READ.LOGIN, true, false, 3)   // Ưu tiên 3: Đăng nhập (Của mình - hiếm)
		add(STATUS_READ.LOGIN, false, true, 4)   // Ưu tiên 4: Đăng nhập (Mới tinh - Trống)
		
		// Login Reset thì lấy thêm cả Completed để chạy lại
		if action == "login_reset" {
			add(STATUS_READ.COMPLETED, true, false, 5)
		}
	
	// 2. Nhóm Register
	// Thứ tự: Đang đk -> Chờ đk -> Đăng ký
	} else if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1) // Đang đăng ký
		add(STATUS_READ.WAIT_REG, true, false, 2)    // Chờ đăng ký
		add(STATUS_READ.REGISTER, true, false, 3)    // Đăng ký (Của mình)
		add(STATUS_READ.REGISTER, false, true, 4)    // Đăng ký (Mới tinh)

	// 3. Nhóm Auto / Auto Reset (Kết hợp Login trước -> Reg sau)
	} else if action == "auto" || action == "auto_reset" {
		// --- Phần Login ---
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		if action == "auto_reset" {
			add(STATUS_READ.COMPLETED, true, false, 99) // Auto reset cũng lấy lại Completed
		}
		
		// --- Phần Register ---
		add(STATUS_READ.REGISTERING, true, false, 5)
		add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7)
		add(STATUS_READ.REGISTER, false, true, 8)
	}

	return steps
}

// commit_and_response: Ghi trạng thái mới vào Cache/Queue và trả về JSON cho User
func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string, priority int) (*LoginResponse, error) {
	row := cache.RawValues[idx]
	
	// Xác định Status ghi xuống
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }

	// Xử lý Note (Ghi chú)
	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false
	
	// Logic Reset: Nếu action chứa reset VÀ lấy ở bước Completed -> Note kiểu reset
	if (strings.Contains(action, "reset")) && (priority == 5 || priority == 99) {
		mode = "reset"
		isResetCompleted = true
	}
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	
	// Dọn dẹp các nick cũ đang chạy dở của Device này (để tránh 1 device chạy nhiều nick cùng lúc)
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		cNote := tao_ghi_chu_chuan(cOldNote, cSt, "normal")
		if isResetCompleted { cNote = tao_ghi_chu_chuan(cOldNote, "Reset chờ chạy", "reset") }
		
		// Cập nhật Cache nick cũ
		oldCSt := cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS]
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = cSt
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = cNote
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = CleanString(cSt) }
		if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = CleanString(cNote) }
		
		// Sync Map Status
		if oldCSt != CleanString(cSt) {
			removeFromStatusMap(cache.StatusMap, oldCSt, cIdx)
			newCSt := CleanString(cSt)
			cache.StatusMap[newCSt] = append(cache.StatusMap[newCSt], cIdx)
		}
		
		// Đẩy nick cũ vào Queue để ghi xuống Sheet
		cRow := make([]interface{}, len(cache.RawValues[cIdx])); copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// Cập nhật Nick mới (Nick vừa lấy)
	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = tSt
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = tNote
	cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt) }
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(tNote) }
	
	// Sync Map Status cho nick mới
	if oldCleanSt != CleanString(tSt) {
		removeFromStatusMap(cache.StatusMap, oldCleanSt, idx)
		newSt := CleanString(tSt)
		cache.StatusMap[newSt] = append(cache.StatusMap[newSt], idx)
	}
	STATE.SheetMutex.Unlock()

	// Đẩy nick mới vào Queue
	newRow := make([]interface{}, len(row)); copy(newRow, row)
	newRow[INDEX_DATA_TIKTOK.STATUS] = tSt
	newRow[INDEX_DATA_TIKTOK.NOTE] = tNote
	newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	msg := "Lấy nick thành công"
	return &LoginResponse{
		Status: "true", Type: typ, Messenger: msg, DeviceId: deviceId, RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: MakeAuthProfile(newRow), ActivityProfile: MakeActivityProfile(newRow), AiProfile: MakeAiProfile(newRow),
	}, nil
}

// Các hàm helper nhỏ
func checkStatusIsValid(currentStatus, action string) bool {
	// (Giữ logic cũ để dùng cho double check nếu cần, dù logic chính đã lọc theo statusMap)
	// Về cơ bản logic PriorityStep đã cover rồi, hàm này chỉ để check chéo
	return true 
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
	for _, st := range checkList {
		indices := cache.StatusMap[st]
		for _, idx := range indices {
			if idx != targetIdx && idx < len(cache.CleanValues) {
				if cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId { list = append(list, idx) }
			}
		}
	}
	return list
}

func doSelfHealing(sid string, idx int, missing string, cache *SheetCacheData) {
	msg := "Nick thiếu " + missing + "\n" + time.Now().Format("02/01/2006 15:04:05")
	STATE.SheetMutex.Lock()
	if idx < len(cache.RawValues) {
		cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = STATUS_WRITE.ATTENTION
		cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = msg
		if idx < len(cache.CleanValues) && INDEX_DATA_TIKTOK.STATUS < len(cache.CleanValues[idx]) {
			oldSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
			removeFromStatusMap(cache.StatusMap, oldSt, idx)
			cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(STATUS_WRITE.ATTENTION)
		}
	}
	fullRow := make([]interface{}, len(cache.RawValues[idx])); copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()
	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

func tao_ghi_chu_chuan(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" { return fmt.Sprintf("%s\n%s", newStatus, nowFull) }
	count := 0; oldNote = strings.TrimSpace(oldNote); lines := strings.Split(oldNote, "\n")
	if idx := strings.Index(oldNote, "(Lần"); idx != -1 {
		end := strings.Index(oldNote[idx:], ")"); if end != -1 { fmt.Sscanf(oldNote[idx+len("(Lần"):idx+end], "%d", &count) }
	}
	if count == 0 { count = 1 }
	today := nowFull[:10]; oldDate := ""
	for _, l := range lines { if len(l) >= 10 && strings.Contains(l, "/") { oldDate = l[:10]; break } }
	if oldDate != today { count = 1 } else { if mode == "reset" { count++ } else if count == 0 { count = 1 } }
	st := newStatus; if st == "" && len(lines) > 0 { st = lines[0] }
	if st == "" { st = "Đang chạy" }
	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
