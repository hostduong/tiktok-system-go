package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

/*
=================================================================================================
📘 TÀI LIỆU HƯỚNG DẪN REQUEST BODY (API DOCUMENTATION)
=================================================================================================
Endpoint: POST /tool/login

1. CẤU TRÚC CƠ BẢN:
{
    "type": "login" | "register" | "auto" | "view",  // Loại hành động
    "action": "reset",                               // (Tùy chọn) Nếu có, sẽ tìm cả nick đã Hoàn thành để chạy lại
    "deviceId": "device_123",                        // ID thiết bị (Bắt buộc)
    "row_index": 100,                                // (Tùy chọn) Lấy chính xác dòng số 100
}

2. CẤU TRÚC BỘ LỌC NÂNG CAO (ADVANCED FILTER):
Dùng để tìm kiếm nick theo điều kiện. Logic: (Thỏa mãn nhóm AND) VÀ (Thỏa mãn nhóm OR)

{
    "and": {  // Nhóm AND: Nick phải thỏa mãn TẤT CẢ điều kiện trong này
        "match_col_3": ["US", "UK"],  // Cột 3 phải là US hoặc UK
        "min_col_10": 1000            // VÀ Cột 10 phải >= 1000
    },
    "or": {   // Nhóm OR: Nick chỉ cần thỏa mãn ÍT NHẤT MỘT điều kiện trong này
        "contains_col_5": "vip",      // Cột 5 chứa "vip"
        "max_col_6": 50               // HOẶC Cột 6 <= 50
    }
}
=================================================================================================
*/

// =================================================================================================
// 🟢 CẤU TRÚC DỮ LIỆU (STRUCTS)
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

// PriorityStep: Định nghĩa một bước tìm kiếm trong quy trình ưu tiên
type PriorityStep struct {
	Status  string // Trạng thái cần tìm (vd: "đang chạy")
	IsMy    bool   // true: Tìm nick đã gán cho mình. false: Tìm nick chung/trống.
	IsEmpty bool   // true: Tìm nick chưa có DeviceId.
	PrioID  int    // Độ ưu tiên (1 cao nhất). Dùng để log hoặc debug.
}

// =================================================================================================
// 🟢 HANDLER CHÍNH: TIẾP NHẬN & ĐIỀU PHỐI REQUEST
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON Body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}

	// 2. Xác thực Token từ Context (Middleware đã làm việc này)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		return // Dừng nếu không có quyền
	}

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])

	// 3. Xử lý cờ Reset (Chạy lại nick đã xong)
	isReset := false
	if reqAction, _ := body["action"].(string); CleanString(reqAction) == "reset" {
		isReset = true
		body["is_reset"] = true // Đẩy lại vào body để truyền xuống các hàm con
	}

	// 4. Phân loại Action (Hành động) chuẩn xác
	action := "login" // Mặc định

	if reqType == "view" {
		action = "view_only"
	} else if reqType == "register" {
		action = "register"
	} else if reqType == "auto" {
		action = "auto"
	} else {
		// Nhóm Login
		if isReset {
			action = "login_reset" // Kích hoạt tìm kiếm nick Completed
		} else {
			action = "login"
		}
	}

	// 5. Gọi hàm xử lý nghiệp vụ chính
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)

	// 6. Trả về kết quả
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 CORE LOGIC: TÌM KIẾM VÀ XỬ LÝ DỮ LIỆU
// =================================================================================================

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	// 1. Tải dữ liệu từ Cache RAM (Tốc độ cao)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}

	// 2. Parse Row Index (Nếu client chỉ định đích danh)
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			rowIndexInput = int(val)
		}
	}

	// 3. Parse Bộ lọc Nâng cao (AND/OR Logic) -> Gọi hàm từ utils.go
	filters := parseFilterParams(body)

	// Bắt đầu vùng Lock Đọc (Cho phép nhiều người đọc cùng lúc)
	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues)

	// ---------------------------------------------------------------------------------------------
	// 📍 NHÁNH 1: ƯU TIÊN TUYỆT ĐỐI (ROW INDEX)
	// ---------------------------------------------------------------------------------------------
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < rawLen {
			cleanRow := cacheData.CleanValues[idx]
			row := cacheData.RawValues[idx]

			// Kiểm tra xem dòng này có thỏa mãn bộ lọc không (Nếu có lọc)
			if filters.HasFilter {
				if !isRowMatched(cleanRow, row, filters) {
					STATE.SheetMutex.RUnlock()
					return nil, fmt.Errorf("row_index không đủ điều kiện")
				}
			}

			// Kiểm tra chất lượng (Đủ user/pass/email...)
			val := KiemTraChatLuongClean(cleanRow, action)
			if val.Valid {
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cleanRow), val.SystemEmail, action, 0)
			} else {
				STATE.SheetMutex.RUnlock()
				return nil, fmt.Errorf("row_index tài khoản lỗi: %s", val.Missing)
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Dòng yêu cầu không tồn tại")
	}

	// ---------------------------------------------------------------------------------------------
	// 📍 NHÁNH 2: TÌM KIẾM NÂNG CAO (ADVANCED FILTER)
	// ---------------------------------------------------------------------------------------------
	if filters.HasFilter {
		for i, cleanRow := range cacheData.CleanValues {
			// B1: Kiểm tra Dữ liệu (Fail Fast)
			if !isRowMatched(cleanRow, cacheData.RawValues[i], filters) {
				continue
			}

			// B2: CHỐT CHẶN TRẠNG THÁI (Status Guard)
			if !checkStatusIsValid(cleanRow[INDEX_DATA_TIKTOK.STATUS], action) {
				continue
			}

			// B3: Kiểm tra Quyền sở hữu
			curDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
			if curDev != "" && curDev != deviceId {
				continue
			}

			// B4: Kiểm tra Chất lượng Nick
			val := KiemTraChatLuongClean(cleanRow, action)
			if val.Valid {
				// --- 🛡️ DOUBLE CHECK LOCKING ---
				STATE.SheetMutex.RUnlock()
				STATE.SheetMutex.Lock()

				currCleanRow := cacheData.CleanValues[i]
				currDev := currCleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
				currStatus := currCleanRow[INDEX_DATA_TIKTOK.STATUS]

				if (currDev == "" || currDev == deviceId) && checkStatusIsValid(currStatus, action) {
					cacheData.CleanValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
					cacheData.RawValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
					cacheData.AssignedMap[deviceId] = i

					STATE.SheetMutex.Unlock()
					return commit_and_response(sid, deviceId, cacheData, i, determineType(currCleanRow), val.SystemEmail, action, 0)
				}
				STATE.SheetMutex.Unlock()
				STATE.SheetMutex.RLock()
			} else {
				STATE.SheetMutex.RUnlock()
				doSelfHealing(sid, i, val.Missing, cacheData)
				STATE.SheetMutex.RLock()
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo điều kiện")
	}

	// ---------------------------------------------------------------------------------------------
	// 📍 NHÁNH 3: TỰ ĐỘNG (AUTO / PRIORITY)
	// ---------------------------------------------------------------------------------------------
	if action != "view_only" {
		isReset := false
		if v, ok := body["is_reset"].(bool); ok && v {
			isReset = true
		}
		if action == "login_reset" {
			isReset = true
		}

		steps := buildPrioritySteps(action, isReset)

		for _, step := range steps {
			indices := cacheData.StatusMap[step.Status]

			for _, idx := range indices {
				if idx < rawLen {
					row := cacheData.CleanValues[idx]
					curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
					isMyNick := (curDev == deviceId)
					isEmptyNick := (curDev == "")

					if (step.IsMy && isMyNick) || (step.IsEmpty && isEmptyNick) {
						val := KiemTraChatLuongClean(row, action)
						if !val.Valid {
							STATE.SheetMutex.RUnlock()
							doSelfHealing(sid, idx, val.Missing, cacheData)
							STATE.SheetMutex.RLock()
							continue
						}

						STATE.SheetMutex.RUnlock()
						STATE.SheetMutex.Lock()

						currentRealDev := cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
						if (step.IsMy && currentRealDev == deviceId) || (step.IsEmpty && currentRealDev == "") {
							cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.AssignedMap[deviceId] = idx

							STATE.SheetMutex.Unlock()
							return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID)
						}
						STATE.SheetMutex.Unlock()
						STATE.SheetMutex.RLock()
					}
				}
			}
		}
	}

	// Logic báo lỗi cuối cùng
	checkList := []string{"login", "auto", "login_reset", "register"}
	isCheck := false
	for _, s := range checkList {
		if strings.Contains(action, s) {
			isCheck = true
			break
		}
	}

	if isCheck {
		completedIndices := cacheData.StatusMap[STATUS_READ.COMPLETED]
		hasCompletedNick := false
		for _, idx := range completedIndices {
			if idx < rawLen && cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
				hasCompletedNick = true
				break
			}
		}
		STATE.SheetMutex.RUnlock()
		if hasCompletedNick {
			return nil, fmt.Errorf("Các tài khoản đã hoàn thành")
		}
	} else {
		STATE.SheetMutex.RUnlock()
	}

	return nil, fmt.Errorf("Không còn tài khoản phù hợp")
}

// =================================================================================================
// 🛠 CÁC HÀM HỖ TRỢ KHÁC (STATUS, PRIORITY, CLEANUP)
// =================================================================================================

func checkStatusIsValid(currentStatus, action string) bool {
	if action == "register" {
		if currentStatus == STATUS_READ.REGISTER || currentStatus == STATUS_READ.REGISTERING || currentStatus == STATUS_READ.WAIT_REG {
			return true
		}
	} else if action == "login" || action == "login_reset" {
		if currentStatus == STATUS_READ.LOGIN || currentStatus == STATUS_READ.RUNNING || currentStatus == STATUS_READ.WAITING {
			return true
		}
		if (action == "login_reset") && currentStatus == STATUS_READ.COMPLETED {
			return true
		}
	} else if action == "auto" {
		return true // Auto chấp nhận tất cả
	} else {
		return true // View only
	}
	return false
}

func buildPrioritySteps(action string, isReset bool) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	if strings.Contains(action, "login") {
		add(STATUS_READ.RUNNING, true, false, 1); add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3); add(STATUS_READ.LOGIN, false, true, 4)
		if isReset { add(STATUS_READ.COMPLETED, true, false, 5) }
	} else if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1); add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3); add(STATUS_READ.REGISTER, false, true, 4)
	} else if action == "auto" {
		add(STATUS_READ.RUNNING, true, false, 1); add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3); add(STATUS_READ.LOGIN, false, true, 4)
		add(STATUS_READ.REGISTERING, true, false, 5); add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7); add(STATUS_READ.REGISTER, false, true, 8)
		if isReset { add(STATUS_READ.COMPLETED, true, false, 9) }
	}
	return steps
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

func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string, priority int) (*LoginResponse, error) {
	if action == "view_only" {
		row := cache.RawValues[idx]
		return &LoginResponse{
			Status: "true", Type: typ, Messenger: "OK", DeviceId: deviceId,
			RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
			AuthProfile: MakeAuthProfile(row), ActivityProfile: MakeActivityProfile(row), AiProfile: MakeAiProfile(row),
		}, nil
	}

	row := cache.RawValues[idx]
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }

	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false
	if (strings.Contains(action, "auto") || strings.Contains(action, "login_reset")) && (priority == 5 || priority == 9) {
		mode = "reset"; isResetCompleted = true
	}
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		cNote := tao_ghi_chu_chuan(cOldNote, cSt, "normal")
		if isResetCompleted { cNote = tao_ghi_chu_chuan(cOldNote, "Reset chờ chạy", "reset") }

		oldCSt := cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS]
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = cSt
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = cNote
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = CleanString(cSt) }
		if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = CleanString(cNote) }
		if oldCSt != CleanString(cSt) {
			removeFromStatusMap(cache.StatusMap, oldCSt, cIdx)
			newCSt := CleanString(cSt)
			cache.StatusMap[newCSt] = append(cache.StatusMap[newCSt], cIdx)
		}
		cRow := make([]interface{}, len(cache.RawValues[cIdx]))
		copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = tSt
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = tNote
	cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt) }
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(tNote) }
	if oldCleanSt != CleanString(tSt) {
		removeFromStatusMap(cache.StatusMap, oldCleanSt, idx)
		newSt := CleanString(tSt)
		cache.StatusMap[newSt] = append(cache.StatusMap[newSt], idx)
	}
	STATE.SheetMutex.Unlock()

	newRow := make([]interface{}, len(row))
	copy(newRow, row)
	newRow[INDEX_DATA_TIKTOK.STATUS] = tSt
	newRow[INDEX_DATA_TIKTOK.NOTE] = tNote
	newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	msg := "Lấy nick đăng nhập thành công"
	if typ == "register" { msg = "Lấy nick đăng ký thành công" }
	return &LoginResponse{
		Status: "true", Type: typ, Messenger: msg, DeviceId: deviceId,
		RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: MakeAuthProfile(newRow), ActivityProfile: MakeActivityProfile(newRow), AiProfile: MakeAiProfile(newRow),
	}, nil
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
		end := strings.Index(oldNote[idx:], ")")
		if end != -1 { fmt.Sscanf(oldNote[idx+len("(Lần"):idx+end], "%d", &count) }
	}
	if count == 0 { count = 1 }
	today := nowFull[:10]; oldDate := ""
	for _, l := range lines { if len(l) >= 10 && strings.Contains(l, "/") { oldDate = l[:10]; break } }
	if oldDate != today { count = 1 } else {
		if mode == "reset" { count++ } else if count == 0 { count = 1 }
	}
	st := newStatus; if st == "" && len(lines) > 0 { st = lines[0] }
	if st == "" { st = "Đang chạy" }
	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
