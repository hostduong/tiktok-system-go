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
	IsMy    bool
	IsEmpty bool
	PrioID  int
}

// =================================================================================================
// 🟢 HANDLER CHÍNH
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}

	// DEBUG LOG
	fmt.Printf("\n🔵 [REQUEST BODY]: %+v\n", body)

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	// Chuẩn hóa Action từ Type
	action := "login"
	if reqType == "register" { action = "register" } else if reqType == "auto" { action = "auto" } else if reqType == "auto_reset" { action = "auto_reset" } else if reqType == "login_reset" { action = "login_reset" }
	
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		fmt.Printf("🔴 [ERROR]: %s\n", err.Error())
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	fmt.Println("🟢 [SUCCESS]")
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 CORE LOGIC
// =================================================================================================

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	// 1. Parse Filters (search_and / search_or)
	filters := parseFilterParams(body)

	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues)

	// --- 📍 CHIẾN LƯỢC 1: ROW INDEX (Ưu tiên tuyệt đối) ---
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			idx := int(val) - RANGES.DATA_START_ROW
			if idx >= 0 && idx < rawLen {
				// Nếu có Filter -> Phải khớp mới lấy
				if filters.HasFilter {
					if !isRowMatched(cacheData.CleanValues[idx], cacheData.RawValues[idx], filters) {
						STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("row_index không khớp điều kiện tìm kiếm")
					}
				}
				// LẤY LUÔN (Bỏ qua check status, quality vì user chỉ định)
				valQ := KiemTraChatLuongClean(cacheData.CleanValues[idx], action) // Chỉ để lấy email
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), valQ.SystemEmail, action, 0)
			}
			STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("row_index không tồn tại")
		}
	}

	// --- 📍 CHIẾN LƯỢC 2: TỰ ĐỘNG THEO QUY TRÌNH (PRIORITY STEPS) ---
	// Logic: Duyệt từng bước -> Lọc theo Status -> (Lọc Search nếu có) -> Lấy
	
	steps := buildPrioritySteps(action)

	for _, step := range steps {
		indices := cacheData.StatusMap[step.Status]
		
		for _, idx := range indices {
			if idx < rawLen {
				row := cacheData.CleanValues[idx]
				
				// 1. Check Device (Của mình hoặc Rỗng)
				isMyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId)
				isEmptyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == "")
				
				if (step.IsMy && isMyDevice) || (step.IsEmpty && isEmptyDevice) {
					
					// 2. Check Search (Nếu có search_and / search_or)
					// Nếu không có search (HasFilter=false) -> Hàm isRowMatched trả về True -> OK
					if filters.HasFilter {
						if !isRowMatched(row, cacheData.RawValues[idx], filters) { continue }
					}
					
					// 3. Check Quality
					val := KiemTraChatLuongClean(row, action)
					if !val.Valid {
						// Nếu nick lỗi -> Tự sửa (Self Healing)
						STATE.SheetMutex.RUnlock(); doSelfHealing(sid, idx, val.Missing, cacheData); STATE.SheetMutex.RLock()
						continue
					}

					// -> THỎA MÃN TẤT CẢ -> CHỐT ĐƠN
					STATE.SheetMutex.RUnlock(); STATE.SheetMutex.Lock()
					
					// Double Check (Tránh Race Condition)
					currRow := cacheData.CleanValues[idx]
					if (step.IsMy && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
						// Update Owner
						cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
						cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
						cacheData.AssignedMap[deviceId] = idx
						STATE.SheetMutex.Unlock()
						
						return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID)
					}
					STATE.SheetMutex.Unlock(); STATE.SheetMutex.RLock()
				}
			}
		}
	}
	
	// Check Completed (Nếu đã hết nick chạy, kiểm tra xem có nick nào đã hoàn thành không để báo lỗi chuẩn)
	// (Logic này giữ nguyên để Client biết đường dừng)
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
// 🛠 CÁC HÀM HỖ TRỢ
// =================================================================================================

func buildPrioritySteps(action string) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	// Hàm helper thêm bước
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	// 1. Nhóm Login / Login Reset
	if action == "login" || action == "login_reset" {
		add(STATUS_READ.RUNNING, true, false, 1) // Đang chạy
		add(STATUS_READ.WAITING, true, false, 2) // Đang chờ
		add(STATUS_READ.LOGIN, true, false, 3)   // Đăng nhập
		add(STATUS_READ.LOGIN, false, true, 4)   // Đăng nhập (Mới)
		if action == "login_reset" {
			add(STATUS_READ.COMPLETED, true, false, 5) // Hoàn thành (Chạy lại)
		}
	
	// 2. Nhóm Register
	} else if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1) // Đang đk
		add(STATUS_READ.WAIT_REG, true, false, 2)    // Chờ đk
		add(STATUS_READ.REGISTER, true, false, 3)    // Đăng ký
		add(STATUS_READ.REGISTER, false, true, 4)    // Đăng ký (Mới)

	// 3. Nhóm Auto / Auto Reset (Login trước -> Reg sau)
	} else if action == "auto" || action == "auto_reset" {
		// Phần Login
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		if action == "auto_reset" {
			add(STATUS_READ.COMPLETED, true, false, 99) // Auto reset cũng lấy lại Completed
		}
		// Phần Register
		add(STATUS_READ.REGISTERING, true, false, 5)
		add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7)
		add(STATUS_READ.REGISTER, false, true, 8)
	}

	return steps
}

// ... (Giữ nguyên các hàm checkStatusIsValid, determineType, getCleanupIndices, commit_and_response, doSelfHealing, tao_ghi_chu_chuan...)
// Copy y nguyên phần dưới của file cũ vào đây. Lưu ý logic tao_ghi_chu_chuan cần khớp với action reset.

func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string, priority int) (*LoginResponse, error) {
	row := cache.RawValues[idx]
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }

	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false
	
	// Logic Reset: Nếu action chứa reset VÀ lấy ở bước Completed -> Note kiểu reset
	if (strings.Contains(action, "reset")) && priority >= 5 {
		mode = "reset"
		isResetCompleted = true
	}
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	// ... (Đoạn cleanup giữ nguyên) ...
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		cNote := tao_ghi_chu_chuan(cOldNote, cSt, "normal")
		if isResetCompleted { cNote = tao_ghi_chu_chuan(cOldNote, "Reset chờ chạy", "reset") }
		
		// Update Cache & Queue...
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = cSt
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = cNote
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = CleanString(cSt) }
		if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = CleanString(cNote) }
		
		// Map Sync...
		// QueueUpdate...
		cRow := make([]interface{}, len(cache.RawValues[cIdx])); copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// Update Target Row
	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = tSt
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = tNote
	cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt) }
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(tNote) }
	
	// Map Sync...
	if oldCleanSt != CleanString(tSt) {
		removeFromStatusMap(cache.StatusMap, oldCleanSt, idx)
		newSt := CleanString(tSt)
		cache.StatusMap[newSt] = append(cache.StatusMap[newSt], idx)
	}
	STATE.SheetMutex.Unlock()

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

// ... Copy các hàm getCleanupIndices, doSelfHealing, tao_ghi_chu_chuan, determineType, checkStatusIsValid từ file cũ vào đây (Logic không đổi)
// ...
