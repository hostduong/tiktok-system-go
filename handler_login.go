package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ... (Giữ nguyên Struct LoginResponse và PriorityStep) ...
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

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
    // ... (Giữ nguyên phần decode body và init variables) ...
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}
	fmt.Printf("\n🔵 [REQUEST BODY]: %+v\n", body)

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])

	isReset := false
	if reqAction, _ := body["action"].(string); CleanString(reqAction) == "reset" {
		isReset = true; body["is_reset"] = true
	}

	action := "login"
	if reqType == "view" { action = "view_only" } else if reqType == "register" { action = "register" } else if reqType == "auto" { action = "auto" } else {
		if isReset { action = "login_reset" } else { action = "login" }
	}

	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		fmt.Printf("🔴 [ERROR RETURN]: %s\n", err.Error()) // Log lỗi trả về
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	fmt.Println("🟢 [SUCCESS]")
	json.NewEncoder(w).Encode(res)
}

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	rowIndexInput := -1
	if v, ok := body["row_index"]; ok { if val, ok := toFloat(v); ok { rowIndexInput = int(val) } }

	filters := parseFilterParams(body)

	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues)

    // ... (Nhánh 1 Row Index giữ nguyên) ...
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < rawLen {
			if filters.HasFilter { if !isRowMatched(cacheData.CleanValues[idx], cacheData.RawValues[idx], filters) { STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("row_index không đủ điều kiện") } }
			val := KiemTraChatLuongClean(cacheData.CleanValues[idx], action)
			if val.Valid { STATE.SheetMutex.RUnlock(); return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, 0) }
			STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("row_index tài khoản lỗi: %s", val.Missing)
		}
		STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Dòng yêu cầu không tồn tại")
	}

	// ---------------------------------------------------------------------------------------------
	// 📍 NHÁNH 2: TÌM KIẾM NÂNG CAO (FILTER) - DEBUG MODE
	// ---------------------------------------------------------------------------------------------
	if filters.HasFilter {
		fmt.Println("\n🔎 [FILTER START] Đang quét...")
		
		for i, cleanRow := range cacheData.CleanValues {
			// LOGIC CŨ
			if !isRowMatched(cleanRow, cacheData.RawValues[i], filters) { continue }
			if !checkStatusIsValid(cleanRow[INDEX_DATA_TIKTOK.STATUS], action) { continue }
			
			curDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
			if curDev != "" && curDev != deviceId { continue }

			// 🔥 NẾU CODE CHẠY ĐẾN ĐÂY NGHĨA LÀ ĐÃ KHỚP FILTER, STATUS, DEVICE
			realIdx := i + RANGES.DATA_START_ROW
			fmt.Printf("👉 [FOUND MATCH] Dòng %d (Status: %s) -> Đang kiểm tra chất lượng...\n", realIdx, cleanRow[INDEX_DATA_TIKTOK.STATUS])

			val := KiemTraChatLuongClean(cleanRow, action)
			if val.Valid {
				fmt.Printf("   ✅ Chất lượng OK. Bắt đầu Double Check (Khóa ghi)...\n")
				
				STATE.SheetMutex.RUnlock()
				STATE.SheetMutex.Lock()
				
				// --- DOUBLE CHECK LOGIC ---
				currCleanRow := cacheData.CleanValues[i]
				
				// Log xem Double Check thấy gì
				chkDev := currCleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
				chkStatus := currCleanRow[INDEX_DATA_TIKTOK.STATUS]
				isDevOk := (chkDev == "" || chkDev == deviceId)
				isStatusOk := checkStatusIsValid(chkStatus, action)
				
				fmt.Printf("   ❓ [DOUBLE CHECK] Dòng %d | DevRAM: '%s' (Req: '%s') | StatusRAM: '%s'\n", realIdx, chkDev, deviceId, chkStatus)
				fmt.Printf("      -> Device OK? %v | Status OK? %v\n", isDevOk, isStatusOk)

				if isDevOk && isStatusOk {
					cacheData.CleanValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
					cacheData.RawValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
					cacheData.AssignedMap[deviceId] = i
					STATE.SheetMutex.Unlock()
					fmt.Println("   🚀 [SUCCESS] CHỐT ĐƠN THÀNH CÔNG!")
					return commit_and_response(sid, deviceId, cacheData, i, determineType(currCleanRow), val.SystemEmail, action, 0)
				} else {
					fmt.Println("   ❌ [FAIL] Tạch ở Double Check! Ai đó đã cướp nick hoặc status đổi.")
				}
				
				STATE.SheetMutex.Unlock()
				STATE.SheetMutex.RLock()
			} else {
				// Nếu chất lượng lỗi -> In ra lỗi gì
				fmt.Printf("   ❌ [FAIL] Chất lượng kém: %s. Đang Self Healing...\n", val.Missing)
				STATE.SheetMutex.RUnlock()
				doSelfHealing(sid, i, val.Missing, cacheData)
				STATE.SheetMutex.RLock()
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo điều kiện")
	}

	// --- NHÁNH 3: AUTO ---
	// ... (Copy logic Auto cũ vào đây giữ nguyên) ...
    if action != "view_only" {
		isReset := false
		if v, ok := body["is_reset"].(bool); ok && v { isReset = true }
		if action == "login_reset" { isReset = true }

		steps := buildPrioritySteps(action, isReset)
        for _, step := range steps {
            indices := cacheData.StatusMap[step.Status]
            for _, idx := range indices {
                if idx < rawLen {
                    row := cacheData.CleanValues[idx]
					if (step.IsMy && row[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && row[INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
						val := KiemTraChatLuongClean(row, action)
						if !val.Valid { STATE.SheetMutex.RUnlock(); doSelfHealing(sid, idx, val.Missing, cacheData); STATE.SheetMutex.RLock(); continue }
						STATE.SheetMutex.RUnlock(); STATE.SheetMutex.Lock()
						if (step.IsMy && cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
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
    }

	STATE.SheetMutex.RUnlock()
	return nil, fmt.Errorf("Không còn tài khoản phù hợp")
}

// ... (Giữ nguyên các hàm helper checkStatusIsValid, buildPrioritySteps, commit_and_response...)
func checkStatusIsValid(currentStatus, action string) bool {
	if action == "register" {
		if currentStatus == STATUS_READ.REGISTER || currentStatus == STATUS_READ.REGISTERING || currentStatus == STATUS_READ.WAIT_REG { return true }
	} else if action == "login" || action == "login_reset" {
		if currentStatus == STATUS_READ.LOGIN || currentStatus == STATUS_READ.RUNNING || currentStatus == STATUS_READ.WAITING { return true }
		if (action == "login_reset") && currentStatus == STATUS_READ.COMPLETED { return true }
	} else if action == "auto" {
		return true
	} else {
		return true
	}
	return false
}

func buildPrioritySteps(action string, isReset bool) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	add := func(st string, my, empty bool, prio int) { steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio}) }
	if strings.Contains(action, "login") {
		add(STATUS_READ.RUNNING, true, false, 1); add(STATUS_READ.WAITING, true, false, 2); add(STATUS_READ.LOGIN, true, false, 3); add(STATUS_READ.LOGIN, false, true, 4)
		if isReset { add(STATUS_READ.COMPLETED, true, false, 5) }
	} else if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1); add(STATUS_READ.WAIT_REG, true, false, 2); add(STATUS_READ.REGISTER, true, false, 3); add(STATUS_READ.REGISTER, false, true, 4)
	} else if action == "auto" {
		add(STATUS_READ.RUNNING, true, false, 1); add(STATUS_READ.WAITING, true, false, 2); add(STATUS_READ.LOGIN, true, false, 3); add(STATUS_READ.LOGIN, false, true, 4)
		add(STATUS_READ.REGISTERING, true, false, 5); add(STATUS_READ.WAIT_REG, true, false, 6); add(STATUS_READ.REGISTER, true, false, 7); add(STATUS_READ.REGISTER, false, true, 8)
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
			Status: "true", Type: typ, Messenger: "OK", DeviceId: deviceId, RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
			AuthProfile: MakeAuthProfile(row), ActivityProfile: MakeActivityProfile(row), AiProfile: MakeAiProfile(row),
		}, nil
	}
	row := cache.RawValues[idx]
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }
	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false
	if (strings.Contains(action, "auto") || strings.Contains(action, "login_reset")) && (priority == 5 || priority == 9) { mode = "reset"; isResetCompleted = true }
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
		Status: "true", Type: typ, Messenger: msg, DeviceId: deviceId, RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
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
