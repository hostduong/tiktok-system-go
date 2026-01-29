package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// =================================================================================================
// 🟢 CẤU TRÚC DỮ LIỆU RESPONSE
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

// Cấu trúc chứa các điều kiện lọc nâng cao
type FilterParams struct {
	MatchCols    map[int][]string
	ContainsCols map[int][]string
	MinCols      map[int]float64
	MaxCols      map[int]float64
	TimeCols     map[int]float64 // Hours
	HasFilter    bool
}

// =================================================================================================
// 🟢 HANDLER CHÍNH: PHÂN LOẠI REQUEST
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		return
	}

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"]) // register, login, auto, view...
	
	// --- XỬ LÝ LOGIC RESET ---
	// Kiểm tra xem user có gửi action="reset" không
	isReset := false
	if reqAction, _ := body["action"].(string); CleanString(reqAction) == "reset" {
		isReset = true
		body["is_reset"] = true // Gắn cờ vào body
	}

	// --- PHÂN LOẠI HÀNH ĐỘNG (ACTION) ---
	action := "login" // Mặc định

	if reqType == "view" {
		action = "view_only"
	} else if reqType == "register" {
		action = "register"
		// Lưu ý: Với Register, action="reset" là VÔ TÁC DỤNG.
		// Logic bên dưới (buildPrioritySteps) sẽ không dùng isReset cho nhánh register.
	} else if reqType == "auto" {
		action = "auto"
	} else {
		// Trường hợp Login
		if isReset {
			action = "login_reset"
		} else {
			action = "login"
		}
	}

	// Gọi hàm xử lý lõi
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC LÕI: TÌM KIẾM VÀ TRẢ VỀ NICK
// =================================================================================================

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}

	// 1. Parse row_index
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			rowIndexInput = int(val)
		}
	}

	// 2. Parse Bộ Lọc Nâng Cao
	filters := parseFilterParams(body)

	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues)

	// =================================================================================
	// 🟢 NHÁNH 1: PRIORITY TUYỆT ĐỐI (Dùng row_index)
	// =================================================================================
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < rawLen {
			cleanRow := cacheData.CleanValues[idx]
			row := cacheData.RawValues[idx]

			// Kiểm tra điều kiện lọc
			if filters.HasFilter {
				if !isRowMatched(cleanRow, row, filters) {
					STATE.SheetMutex.RUnlock()
					return nil, fmt.Errorf("row_index không đủ điều kiện") 
				}
			}

			// Kiểm tra chất lượng
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

	// =================================================================================
	// 🟢 NHÁNH 2: ADVANCED SEARCH MODE (Quét toàn bộ nếu có Filter)
	// =================================================================================
	if filters.HasFilter {
		for i, cleanRow := range cacheData.CleanValues {
			// 1. Check bộ lọc
			if !isRowMatched(cleanRow, cacheData.RawValues[i], filters) {
				continue
			}

			// 2. Check quyền sở hữu
			curDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
			if curDev != "" && curDev != deviceId {
				continue
			}

			// 3. Check chất lượng
			val := KiemTraChatLuongClean(cleanRow, action)
			if val.Valid {
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, i, determineType(cleanRow), val.SystemEmail, action, 0)
			} else {
				// Self Healing
				STATE.SheetMutex.RUnlock()
				doSelfHealing(sid, i, val.Missing, cacheData)
				STATE.SheetMutex.RLock()
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo điều kiện")
	}

	// =================================================================================
	// 🟢 NHÁNH 3: AUTO DEFAULT (Unified Priority Loop)
	// =================================================================================
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

						// CLAIM
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
	for _, s := range checkList { if strings.Contains(action, s) { isCheck = true; break } }

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

// ------------------------------------------------------------------------------------------------
// 🛠 BỘ HÀM HỖ TRỢ FILTER & PARSING
// ------------------------------------------------------------------------------------------------

func parseFilterParams(body map[string]interface{}) FilterParams {
	f := FilterParams{
		MatchCols:    make(map[int][]string),
		ContainsCols: make(map[int][]string),
		MinCols:      make(map[int]float64),
		MaxCols:      make(map[int]float64),
		TimeCols:     make(map[int]float64),
		HasFilter:    false,
	}

	for k, v := range body {
		if strings.HasPrefix(k, "match_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "match_col_")); err == nil {
				f.MatchCols[idx] = ToSlice(v)
				f.HasFilter = true
			}
		} else if strings.HasPrefix(k, "contains_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "contains_col_")); err == nil {
				f.ContainsCols[idx] = ToSlice(v)
				f.HasFilter = true
			}
		} else if strings.HasPrefix(k, "min_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "min_col_")); err == nil {
				if val, ok := toFloat(v); ok {
					f.MinCols[idx] = val
					f.HasFilter = true
				}
			}
		} else if strings.HasPrefix(k, "max_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "max_col_")); err == nil {
				if val, ok := toFloat(v); ok {
					f.MaxCols[idx] = val
					f.HasFilter = true
				}
			}
		} else if strings.HasPrefix(k, "last_hours_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "last_hours_col_")); err == nil {
				if val, ok := toFloat(v); ok {
					f.TimeCols[idx] = val
					f.HasFilter = true
				}
			}
		} else if strings.HasPrefix(k, "search_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "search_col_")); err == nil {
				f.MatchCols[idx] = ToSlice(v)
				f.HasFilter = true
			}
		}
	}
	return f
}

func isRowMatched(cleanRow []string, rawRow []interface{}, f FilterParams) bool {
	// 1. Match
	for idx, targets := range f.MatchCols {
		cellVal := ""
		if idx < len(cleanRow) {
			cellVal = cleanRow[idx]
		}
		match := false
		for _, t := range targets {
			if t == cellVal {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	// 2. Contains
	for idx, targets := range f.ContainsCols {
		cellVal := ""
		if idx < len(cleanRow) {
			cellVal = cleanRow[idx]
		}
		match := false
		for _, t := range targets {
			if t == "" {
				if cellVal == "" {
					match = true
					break
				}
			} else {
				if strings.Contains(cellVal, t) {
					match = true
					break
				}
			}
		}
		if !match {
			return false
		}
	}

	// 3. Min/Max
	for idx, minVal := range f.MinCols {
		if val, ok := getFloatVal(rawRow, idx); !ok || val < minVal {
			return false
		}
	}
	for idx, maxVal := range f.MaxCols {
		if val, ok := getFloatVal(rawRow, idx); !ok || val > maxVal {
			return false
		}
	}

	// 4. Time
	now := time.Now().UnixMilli()
	for idx, hours := range f.TimeCols {
		timeVal := int64(0)
		if idx < len(rawRow) {
			timeVal = ConvertSerialDate(rawRow[idx])
		}
		if timeVal == 0 {
			return false
		}
		if float64(now-timeVal)/3600000.0 > hours {
			return false
		}
	}

	return true
}

// ------------------------------------------------------------------------------------------------
// 🟢 CÁC HÀM LOGIC ƯU TIÊN VÀ XỬ LÝ
// ------------------------------------------------------------------------------------------------

func buildPrioritySteps(action string, isReset bool) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	if strings.Contains(action, "login") {
		// Luồng Login
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		if isReset {
			add(STATUS_READ.COMPLETED, true, false, 5) // Login Reset
		}
	} else if action == "register" {
		// Luồng Register (KHÔNG có logic reset ở đây)
		add(STATUS_READ.REGISTERING, true, false, 1)
		add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3)
		add(STATUS_READ.REGISTER, false, true, 4)
		// Đã xóa logic COMPLETED cho register theo yêu cầu
	} else if action == "auto" {
		// Luồng Auto
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		add(STATUS_READ.REGISTERING, true, false, 5)
		add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7)
		add(STATUS_READ.REGISTER, false, true, 8)
		if isReset {
			add(STATUS_READ.COMPLETED, true, false, 9) // Auto Reset (Chỉ áp dụng cho nick login xong)
		}
	}
	return steps
}

func determineType(row []string) string {
	st := row[INDEX_DATA_TIKTOK.STATUS]
	if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG {
		return "register"
	}
	return "login"
}

func getCleanupIndices(cache *SheetCacheData, deviceId string, targetIdx int, isResetCompleted bool) []int {
	var list []int
	checkList := []string{STATUS_READ.RUNNING, STATUS_READ.REGISTERING}
	if isResetCompleted {
		checkList = append(checkList, STATUS_READ.COMPLETED)
	}

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
	if typ == "register" {
		tSt = STATUS_WRITE.REGISTERING
	}

	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false

	// Chỉ kích hoạt chế độ Reset Note khi là Login Reset hoặc Auto Reset
	if (strings.Contains(action, "auto") || strings.Contains(action, "login_reset")) && 
	   (priority == 5 || priority == 9) {
		mode = "reset"
		isResetCompleted = true
	}

	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	
	// --- CLEANUP ---
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)

	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" {
			cSt = STATUS_WRITE.WAIT_REG
		}

		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		cNote := tao_ghi_chu_chuan(cOldNote, cSt, "normal")

		if isResetCompleted {
			cNote = tao_ghi_chu_chuan(cOldNote, "Reset chờ chạy", "reset")
		}

		oldCSt := cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS]
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = cSt
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = cNote

		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
			cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = CleanString(cSt)
		}
		if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT {
			cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = CleanString(cNote)
		}

		if oldCSt != CleanString(cSt) {
			removeFromStatusMap(cache.StatusMap, oldCSt, cIdx)
			newCSt := CleanString(cSt)
			cache.StatusMap[newCSt] = append(cache.StatusMap[newCSt], cIdx)
		}

		cRow := make([]interface{}, len(cache.RawValues[cIdx]))
		copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// --- TARGET UPDATE ---
	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = tSt
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = tNote
	cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId

	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt)
	}
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(tNote)
	}

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
	if typ == "register" {
		msg = "Lấy nick đăng ký thành công"
	}

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
	if mode == "new" {
		return fmt.Sprintf("%s\n%s", newStatus, nowFull)
	}

	count := 0
	oldNote = strings.TrimSpace(oldNote)
	lines := strings.Split(oldNote, "\n")
	if idx := strings.Index(oldNote, "(Lần"); idx != -1 {
		end := strings.Index(oldNote[idx:], ")")
		if end != -1 {
			fmt.Sscanf(oldNote[idx+len("(Lần"):idx+end], "%d", &count)
		}
	}
	if count == 0 {
		count = 1
	}

	today := nowFull[:10]
	oldDate := ""
	for _, l := range lines {
		if len(l) >= 10 && strings.Contains(l, "/") {
			oldDate = l[:10]
			break
		}
	}

	if oldDate != today {
		count = 1
	} else {
		if mode == "reset" {
			count++
		} else if count == 0 {
			count = 1
		}
	}

	st := newStatus
	if st == "" && len(lines) > 0 {
		st = lines[0]
	}
	if st == "" {
		st = "Đang chạy"
	}

	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
