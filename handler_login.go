package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv" // 🔥 ĐÃ THÊM THƯ VIỆN NÀY ĐỂ FIX LỖI BUILD
	"strings"
	"time"
)

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
	DebugLog        string          `json:"debug_log,omitempty"`
}

type PriorityStep struct {
	Status  string
	IsMy    bool
	IsEmpty bool
	PrioID  int
}

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	action := "login"
	if reqType == "register" { action = "register" } else if reqType == "auto" { action = "auto" } else if reqType == "auto_reset" { action = "auto_reset" } else if reqType == "login_reset" { action = "login_reset" }
	
	updateMap := parseUpdateDataLogin(body)

	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action, updateMap)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"status":    "false",
			"messenger": err.Error(),
		})
		return
	}
	json.NewEncoder(w).Encode(res)
}

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string, updateMap map[int]interface{}) (*LoginResponse, error) {
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	filters := parseFilterParams(body)
	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues)

	// Biến ghi log debug
	var traceLog []string
	addLog := func(msg string) { traceLog = append(traceLog, msg) }

	// 1. ROW INDEX
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			idx := int(val) - RANGES.DATA_START_ROW
			if idx >= 0 && idx < rawLen {
				if filters.HasFilter {
					if !isRowMatched(cacheData.CleanValues[idx], cacheData.RawValues[idx], filters) {
						STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("row_index không khớp filter")
					}
				}
				valQ := KiemTraChatLuongClean(cacheData.CleanValues[idx], action)
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), valQ.SystemEmail, action, 0, updateMap)
			}
			STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("row_index không tồn tại")
		}
	}

	// 2. PRIORITY STEPS
	steps := buildPrioritySteps(action)
	addLog(fmt.Sprintf("Start Auto for Device='%s'. Steps: %d", deviceId, len(steps)))

	for _, step := range steps {
		indices := cacheData.StatusMap[step.Status]
		if len(indices) > 0 {
			addLog(fmt.Sprintf("Step '%s' (Prio %d): Found %d rows", step.Status, step.PrioID, len(indices)))
		}

		for _, idx := range indices {
			if idx < rawLen {
				row := cacheData.CleanValues[idx]
				
				// Debug cho dòng 14 (Index 3)
				if idx == 3 { 
					addLog(fmt.Sprintf("--> CHECK ROW 14: Dev='%s' vs Req='%s'", row[INDEX_DATA_TIKTOK.DEVICE_ID], deviceId))
				}

				isMyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId)
				isEmptyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == "")
				
				if (step.IsMy && isMyDevice) || (step.IsEmpty && isEmptyDevice) {
					if filters.HasFilter {
						if !isRowMatched(row, cacheData.RawValues[idx], filters) { 
							if idx == 3 { addLog("--> Row 14 Failed: Filter Mismatch") }
							continue 
						}
					}
					
					val := KiemTraChatLuongClean(row, action)
					if !val.Valid {
						if idx == 3 { addLog(fmt.Sprintf("--> Row 14 Failed: Quality (%s)", val.Missing)) }
						STATE.SheetMutex.RUnlock(); doSelfHealing(sid, idx, val.Missing, cacheData); STATE.SheetMutex.RLock()
						continue
					}

					STATE.SheetMutex.RUnlock(); STATE.SheetMutex.Lock()
					currRow := cacheData.CleanValues[idx]
					if (step.IsMy && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
						cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
						cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
						cacheData.AssignedMap[deviceId] = idx
						STATE.SheetMutex.Unlock()
						return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID, updateMap)
					}
					STATE.SheetMutex.Unlock(); STATE.SheetMutex.RLock()
				} else {
					if idx == 3 { addLog(fmt.Sprintf("--> Row 14 Failed: Device Mismatch (MyConfig=%v, EmptyConfig=%v)", step.IsMy, step.IsEmpty)) }
				}
			}
		}
	}
	
	// Check Completed
	checkList := []string{"login", "auto", "login_reset", "register"}
	isCheck := false
	for _, s := range checkList { if strings.Contains(action, s) { isCheck = true; break } }
	if isCheck {
		completedIndices := cacheData.StatusMap[STATUS_READ.COMPLETED]
		for _, idx := range completedIndices {
			if idx < rawLen && cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
				STATE.SheetMutex.RUnlock()
				return nil, fmt.Errorf("Các tài khoản đã hoàn thành. [DEBUG: %s]", strings.Join(traceLog, " | "))
			}
		}
	}

	STATE.SheetMutex.RUnlock()
	return nil, fmt.Errorf("Không còn tài khoản phù hợp. [DEBUG: %s]", strings.Join(traceLog, " | "))
}

func buildPrioritySteps(action string) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}
	if action == "login" || action == "login_reset" {
		add(STATUS_READ.RUNNING, true, false, 1); add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3); add(STATUS_READ.LOGIN, false, true, 4)
		if action == "login_reset" { add(STATUS_READ.COMPLETED, true, false, 5) }
	} else if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1); add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3); add(STATUS_READ.REGISTER, false, true, 4)
	} else if action == "auto" || action == "auto_reset" {
		add(STATUS_READ.RUNNING, true, false, 1); add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3); add(STATUS_READ.LOGIN, false, true, 4)
		if action == "auto_reset" { add(STATUS_READ.COMPLETED, true, false, 99) }
		add(STATUS_READ.REGISTERING, true, false, 5); add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7); add(STATUS_READ.REGISTER, false, true, 8)
	}
	return steps
}

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
		cRow := make([]interface{}, len(cache.RawValues[cIdx])); copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = tSt
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = tNote
	cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	
	for colIdx, val := range updateMap {
		if colIdx >= 0 && colIdx < len(cache.RawValues[idx]) {
			if colIdx == INDEX_DATA_TIKTOK.STATUS || colIdx == INDEX_DATA_TIKTOK.NOTE || colIdx == INDEX_DATA_TIKTOK.DEVICE_ID { continue }
			cache.RawValues[idx][colIdx] = val
			if colIdx < CACHE.CLEAN_COL_LIMIT {
				cache.CleanValues[idx][colIdx] = CleanString(val)
			}
		}
	}

	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt) }
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(tNote) }
	
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
	
	for colIdx, val := range updateMap {
		if colIdx >= 0 && colIdx < len(newRow) {
			if colIdx == INDEX_DATA_TIKTOK.STATUS || colIdx == INDEX_DATA_TIKTOK.NOTE || colIdx == INDEX_DATA_TIKTOK.DEVICE_ID { continue }
			newRow[colIdx] = val
		}
	}
	
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	msg := "Lấy nick thành công"
	return &LoginResponse{
		Status: "true", Type: typ, Messenger: msg, DeviceId: deviceId, RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: MakeAuthProfile(newRow), ActivityProfile: MakeActivityProfile(newRow), AiProfile: MakeAiProfile(newRow),
	}, nil
}

func parseUpdateDataLogin(body map[string]interface{}) map[int]interface{} {
	cols := make(map[int]interface{})
	if v, ok := body["updated"]; ok {
		if updatedMap, ok := v.(map[string]interface{}); ok {
			for k, val := range updatedMap {
				if strings.HasPrefix(k, "col_") {
					if idxStr := strings.TrimPrefix(k, "col_"); idxStr != "" {
						// 🔥 CẦN strconv ĐỂ CHẠY HÀM Atoi NÀY
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

func checkStatusIsValid(currentStatus, action string) bool { return true }
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
