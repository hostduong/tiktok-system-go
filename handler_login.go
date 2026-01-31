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

// HANDLER CHÍNH
func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400); return
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
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// LOGIC LÕI
func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string, updateMap map[int]interface{}) (*LoginResponse, error) {
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	filters := parseFilterParams(body)
	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues)

	// 1. LẤY THEO ROW INDEX (ƯU TIÊN TUYỆT ĐỐI)
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			idx := int(val) - RANGES.DATA_START_ROW
			if idx >= 0 && idx < rawLen {
				if filters.HasFilter {
					if !isRowMatched(cacheData.CleanValues[idx], cacheData.RawValues[idx], filters) {
						STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Row không khớp Filter")
					}
				}
				valQ := KiemTraChatLuongClean(cacheData.CleanValues[idx], action)
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), valQ.SystemEmail, action, 0, updateMap)
			}
			STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Row không tồn tại")
		}
	}

	// 2. LẤY THEO PHỄU ƯU TIÊN (PRIORITY FUNNEL)
	steps := buildPrioritySteps(action)
	for _, step := range steps {
		indices := cacheData.StatusMap[step.Status]
		for _, idx := range indices {
			if idx < rawLen {
				row := cacheData.CleanValues[idx]
				isMyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId)
				isEmptyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == "")
				
				if (step.IsMy && isMyDevice) || (step.IsEmpty && isEmptyDevice) {
					if filters.HasFilter {
						if !isRowMatched(row, cacheData.RawValues[idx], filters) { continue }
					}
					
					val := KiemTraChatLuongClean(row, action)
					if !val.Valid {
						STATE.SheetMutex.RUnlock(); doSelfHealing(sid, idx, val.Missing, cacheData); STATE.SheetMutex.RLock()
						continue
					}

					STATE.SheetMutex.RUnlock(); STATE.SheetMutex.Lock() // Chuyển sang Write Lock
					currRow := cacheData.CleanValues[idx] // Double Check
					if (step.IsMy && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
						// Gán thiết bị tạm thời trong RAM
						updateRowCache(cacheData, idx, "", "", deviceId)
						STATE.SheetMutex.Unlock()
						return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID, updateMap)
					}
					STATE.SheetMutex.Unlock(); STATE.SheetMutex.RLock()
				}
			}
		}
	}
	
	// 3. CHECK HOÀN THÀNH
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

// CÁC HÀM HỖ TRỢ
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
	tNote := tao_ghi_chu_chuan_login(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	// Dọn dẹp nick cũ
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		cNote := tao_ghi_chu_chuan_login(cOldNote, cSt, "normal")
		if isResetCompleted { cNote = tao_ghi_chu_chuan_login(cOldNote, "Reset chờ chạy", "reset") }
		
		updateRowCache(cache, cIdx, cSt, cNote, "")
		cRow := make([]interface{}, len(cache.RawValues[cIdx])); copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// Update nick mới
	for colIdx, val := range updateMap {
		if colIdx >= 0 && colIdx < len(cache.RawValues[idx]) {
			if colIdx == INDEX_DATA_TIKTOK.STATUS || colIdx == INDEX_DATA_TIKTOK.NOTE || colIdx == INDEX_DATA_TIKTOK.DEVICE_ID { continue }
			cache.RawValues[idx][colIdx] = val
			if colIdx < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][colIdx] = CleanString(val) }
		}
	}
	updateRowCache(cache, idx, tSt, tNote, deviceId)

	newRow := make([]interface{}, len(cache.RawValues[idx])); copy(newRow, cache.RawValues[idx])
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	msg := "Lấy nick thành công"
	return &LoginResponse{
		Status: "true", Type: typ, Messenger: msg, DeviceId: deviceId, RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: MakeAuthProfile(newRow), ActivityProfile: MakeActivityProfile(newRow), AiProfile: MakeAiProfile(newRow),
	}, nil
}

// Đồng bộ RAM (Rất quan trọng)
func updateRowCache(cache *SheetCacheData, idx int, newSt, newNote, newDev string) {
	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	oldDev := cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]

	if newSt != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = newSt }
	if newNote != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = newNote }
	if newDev != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = newDev }

	if newSt != "" && INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(newSt) }
	if newNote != "" && INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(newNote) }
	if newDev != "" && INDEX_DATA_TIKTOK.DEVICE_ID < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(newDev) }

	if newSt != "" {
		newStClean := CleanString(newSt)
		if oldCleanSt != newStClean {
			removeFromStatusMap(cache.StatusMap, oldCleanSt, idx)
			cache.StatusMap[newStClean] = append(cache.StatusMap[newStClean], idx)
		}
	}
	if newDev != "" {
		newDevClean := CleanString(newDev)
		if oldDev != newDevClean {
			if oldDev != "" { delete(cache.AssignedMap, oldDev) } else { removeFromIntList(&cache.UnassignedList, idx) }
			if newDev != "" { cache.AssignedMap[newDevClean] = idx } else { cache.UnassignedList = append(cache.UnassignedList, idx) }
		}
	}
}

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
		updateRowCache(cache, idx, STATUS_WRITE.ATTENTION, msg, "")
	}
	fullRow := make([]interface{}, len(cache.RawValues[idx])); copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()
	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

// Logic tạo Note LOGIN: Tăng số lần nếu reset
func tao_ghi_chu_chuan_login(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" { return fmt.Sprintf("%s\n%s", newStatus, nowFull) }
	
	oldNote = SafeString(oldNote)
	count := 1
	match := REGEX_COUNT.FindStringSubmatch(oldNote)
	if len(match) > 1 { if c, err := strconv.Atoi(match[1]); err == nil { count = c } }

	today := nowFull[:10]; oldDate := ""
	lines := strings.Split(oldNote, "\n")
	for _, l := range lines { 
		if m := REGEX_DATE.FindString(l); m != "" { oldDate = m; break }
	}

	if oldDate != today { count = 1 } else { if mode == "reset" { count++ } }

	st := newStatus
	if st == "" && len(lines) > 0 { st = lines[0] }
	if st == "" { st = "Đang chạy" }
	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
