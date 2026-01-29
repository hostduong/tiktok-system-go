package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
}

type PriorityStep struct {
	Status  string
	IsMy    bool
	IsEmpty bool
	PrioID  int
}

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	action := "login"
	if reqType == "view" { 
		action = "view_only" 
	} else if reqType == "auto" {
		action = "auto"
		if reqAction, _ := body["action"].(string); CleanString(reqAction) == "reset" {
			body["is_reset"] = true
		}
	} else if reqType == "register" { 
		action = "register" 
	} else if reqAction, _ := body["action"].(string); CleanString(reqAction) == "reset" { 
		action = "login_reset" 
	}

	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)
	
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		// Dùng toFloat từ utils.go
		if val, ok := toFloat(v); ok { rowIndexInput = int(val) }
	}

	searchCols := make(map[int]string)
	for k, v := range body {
		if strings.HasPrefix(k, "search_col_") {
			if idxStr := strings.TrimPrefix(k, "search_col_"); idxStr != "" {
				if i, err := strconv.Atoi(idxStr); err == nil {
					searchCols[i] = CleanString(v)
				}
			}
		}
	}
	hasSearch := len(searchCols) > 0

	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues)

	// A. FAST PATH
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < rawLen {
			cleanRow := cacheData.CleanValues[idx]
			match := true
			if hasSearch {
				for cIdx, val := range searchCols {
					if cIdx >= len(cleanRow) || cleanRow[cIdx] != val { match = false; break }
				}
			}
			if match {
				// Gọi hàm từ utils.go
				val := KiemTraChatLuongClean(cleanRow, action)
				if val.Valid {
					STATE.SheetMutex.RUnlock()
					return commit_and_response(sid, deviceId, cacheData, idx, determineType(cleanRow), val.SystemEmail, action, 0)
				}
			}
		}
	}

	// B. SEARCH MODE
	if hasSearch {
		for i, row := range cacheData.CleanValues {
			match := true
			for cIdx, val := range searchCols {
				if cIdx >= len(row) || row[cIdx] != val { match = false; break }
			}
			if match {
				val := KiemTraChatLuongClean(row, action)
				if val.Valid {
					curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
					if curDev == "" || curDev == deviceId {
						STATE.SheetMutex.RUnlock()
						return commit_and_response(sid, deviceId, cacheData, i, determineType(row), val.SystemEmail, action, 0)
					}
				} else {
					STATE.SheetMutex.RUnlock()
					doSelfHealing(sid, i, val.Missing, cacheData)
					STATE.SheetMutex.RLock()
				}
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo yêu cầu")
	}

	// C. UNIFIED PRIORITY LOOP
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
	if action == "login" || action == "auto" || action == "login_reset" {
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

func buildPrioritySteps(action string, isReset bool) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	if strings.Contains(action, "login") {
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		if isReset { add(STATUS_READ.COMPLETED, true, false, 5) }
	} else if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1)
		add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3)
		add(STATUS_READ.REGISTER, false, true, 4)
	} else if action == "auto" {
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		add(STATUS_READ.REGISTERING, true, false, 5)
		add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7)
		add(STATUS_READ.REGISTER, false, true, 8)
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
				if cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
					list = append(list, idx)
				}
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
	if (action == "auto" || action == "login_reset") && (priority == 5 || priority == 9) {
		mode = "reset"
		isResetCompleted = true
	}
	
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		cNote := ""
		if isResetCompleted {
			cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
			cNote = tao_ghi_chu_chuan(cOldNote, "Reset chờ chạy", "reset")
		}

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

func removeFromStatusMap(m map[string][]int, status string, targetIdx int) {
	if list, ok := m[status]; ok {
		for i, v := range list {
			if v == targetIdx {
				m[status] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
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
	fullRow := make([]interface{}, len(cache.RawValues[idx]))
	copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()
	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

func tao_ghi_chu_chuan(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" { return fmt.Sprintf("%s\n%s", newStatus, nowFull) }
	
	count := 0
	oldNote = strings.TrimSpace(oldNote)
	lines := strings.Split(oldNote, "\n")
	if idx := strings.Index(oldNote, "(Lần"); idx != -1 {
		end := strings.Index(oldNote[idx:], ")")
		if end != -1 { fmt.Sscanf(oldNote[idx+len("(Lần"):idx+end], "%d", &count) }
	}
	if count == 0 { count = 1 }

	today := nowFull[:10]
	oldDate := ""
	for _, l := range lines { if len(l) >= 10 && strings.Contains(l, "/") { oldDate = l[:10]; break } }
	
	if oldDate != today { 
		count = 1 
	} else { 
		if mode == "reset" { count++ } else if count == 0 { count = 1 }
	}

	st := newStatus
	if st == "" && len(lines) > 0 { st = lines[0] }
	if st == "" { st = "Đang chạy" }
	
	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
