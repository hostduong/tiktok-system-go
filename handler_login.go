package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cấu trúc phản hồi JSON cho Client
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

// Cấu trúc định nghĩa các bước ưu tiên tìm kiếm
type PriorityStep struct {
	Status  string // Trạng thái cần tìm (VD: "đang chạy")
	IsMy    bool   // True: Tìm nick của chính thiết bị này
	IsEmpty bool   // True: Tìm nick chưa có chủ (trống deviceId)
	PrioID  int    // ID định danh mức độ ưu tiên (để xác định logic Reset)
}

// [HANDLER] Xử lý request Login/Register/Auto/View từ Client
func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// [TỐI ƯU] Sử dụng Decoder stream để đọc JSON nhanh hơn
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	// Lấy thông tin ngữ cảnh từ Middleware
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	// Chuẩn hóa dữ liệu đầu vào
	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	// Xác định action dựa trên type
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

	// Gọi hàm xử lý logic chính
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)
	
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// [CORE LOGIC] Hàm xử lý tìm kiếm và lấy nick
func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	// 1. Tải dữ liệu từ Cache (Rất nhanh vì đã phân vùng)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	// 2. Parse dữ liệu đầu vào
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok { rowIndexInput = int(val) }
	}

	searchCols := make(map[int]string)
	for k, v := range body {
		if strings.HasPrefix(k, "search_col_") {
			// [TỐI ƯU] Cắt chuỗi thủ công nhanh hơn Regex
			if idxStr := strings.TrimPrefix(k, "search_col_"); idxStr != "" {
				if i, err := strconv.Atoi(idxStr); err == nil {
					searchCols[i] = CleanString(v)
				}
			}
		}
	}
	hasSearch := len(searchCols) > 0

	// =========================================================================================
	// 🔒 [AN TOÀN ĐA LUỒNG]: Bắt đầu khóa ĐỌC (RLock)
	// Để đảm bảo khi đang duyệt Map/Slice không bị crash do luồng khác ghi đè.
	// =========================================================================================
	STATE.SheetMutex.RLock()

	// Cache độ dài mảng để tối ưu vòng lặp
	rawLen := len(cacheData.RawValues)

	// A. ƯU TIÊN 0: FAST PATH (Truy cập trực tiếp theo Row Index)
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < rawLen {
			cleanRow := cacheData.CleanValues[idx]
			
			// Kiểm tra điều kiện Search (nếu có)
			match := true
			if hasSearch {
				for cIdx, val := range searchCols {
					if cIdx >= len(cleanRow) || cleanRow[cIdx] != val { match = false; break }
				}
			}
			
			if match {
				val := kiem_tra_chat_luong_clean(cleanRow, action)
				if val.Valid {
					STATE.SheetMutex.RUnlock() // 🔓 Mở khóa ĐỌC trước khi GHI
					return commit_and_response(sid, deviceId, cacheData, idx, determineType(cleanRow), val.SystemEmail, action, 0)
				}
			}
		}
	}

	// B. ƯU TIÊN 1: CHECK ASSIGNED MAP (Nick cũ đang sở hữu - Truy cập O(1))
	if idx, ok := cacheData.AssignedMap[deviceId]; ok && idx < rawLen {
		cleanRow := cacheData.CleanValues[idx]
		// Double check DeviceID trong RAM để chắc chắn
		if cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
			match := true
			if hasSearch {
				for cIdx, val := range searchCols {
					if cIdx >= len(cleanRow) || cleanRow[cIdx] != val { match = false; break }
				}
			}
			if match {
				val := kiem_tra_chat_luong_clean(cleanRow, action)
				if val.Valid {
					STATE.SheetMutex.RUnlock() // 🔓 Mở khóa ĐỌC
					return commit_and_response(sid, deviceId, cacheData, idx, determineType(cleanRow), val.SystemEmail, action, 0)
				}
			}
		}
	}

	// C. ƯU TIÊN 2: SEARCH MODE (Quét toàn bộ O(N) - Chỉ chạy khi có search_col)
	if hasSearch {
		for i, row := range cacheData.CleanValues {
			match := true
			for cIdx, val := range searchCols {
				if cIdx >= len(row) || row[cIdx] != val { match = false; break }
			}
			
			if match {
				val := kiem_tra_chat_luong_clean(row, action)
				if val.Valid {
					curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
					// Chỉ lấy nếu nick trống HOẶC nick là của mình
					if curDev == "" || curDev == deviceId {
						STATE.SheetMutex.RUnlock() // 🔓 Mở khóa ĐỌC
						return commit_and_response(sid, deviceId, cacheData, i, determineType(row), val.SystemEmail, action, 0)
					}
				} else {
					// Nick lỗi -> Mở khóa đọc tạm thời để gọi SelfHealing (vì nó cần Lock ghi)
					STATE.SheetMutex.RUnlock()
					doSelfHealing(sid, i, val.Missing, cacheData)
					STATE.SheetMutex.RLock() // 🔒 Khóa lại để tiếp tục vòng lặp
				}
			}
		}
		// Không tìm thấy trong search mode
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo yêu cầu")
	}

	// D. ƯU TIÊN 3: UNIFIED PRIORITY LOOP (Vòng lặp ưu tiên chuẩn Node.js)
	if action != "view_only" {
		isReset := false
		if v, ok := body["is_reset"].(bool); ok && v { isReset = true }
		
		// Lấy danh sách các bước ưu tiên
		steps := buildPrioritySteps(action, isReset)
		
		for _, step := range steps {
			// Lấy danh sách index theo Status (O(1) lookup từ Map)
			indices := cacheData.StatusMap[step.Status]
			
			for _, idx := range indices {
				if idx < rawLen {
					row := cacheData.CleanValues[idx]
					curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
					
					// Kiểm tra điều kiện sở hữu (Của mình hoặc Trống)
					isMyNick := (curDev == deviceId)
					isEmptyNick := (curDev == "")
					
					if (step.IsMy && isMyNick) || (step.IsEmpty && isEmptyNick) {
						// Kiểm tra chất lượng nick
						val := kiem_tra_chat_luong_clean(row, action)
						
						if !val.Valid {
							// Nick lỗi -> Ghi chú và bỏ qua
							STATE.SheetMutex.RUnlock()
							doSelfHealing(sid, idx, val.Missing, cacheData)
							STATE.SheetMutex.RLock()
							continue
						}

						// === TÌM THẤY ỨNG VIÊN ===
						STATE.SheetMutex.RUnlock() // 🔓 Nhả khóa ĐỌC
						
						// 🔒 Bắt đầu khóa GHI (Critical Section)
						STATE.SheetMutex.Lock()
						
						// Double Check (Kiểm tra lại lần cuối trong Lock Ghi)
						// Vì trong lúc nhả khóa đọc, có thể luồng khác đã chiếm nick này
						currentRealDev := cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
						
						if (step.IsMy && currentRealDev == deviceId) || (step.IsEmpty && currentRealDev == "") {
							// ✅ CHIẾM QUYỀN (Claim)
							cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.AssignedMap[deviceId] = idx
							
							STATE.SheetMutex.Unlock() // 🔓 Nhả khóa GHI
							
							// Thực hiện commit và trả về
							return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID)
						}
						
						STATE.SheetMutex.Unlock() // 🔓 Nhả khóa GHI (Claim thất bại)
						STATE.SheetMutex.RLock()  // 🔒 Khóa ĐỌC lại để tìm tiếp
					}
				}
			}
		}
	}

	// -----------------------------------------------------------------------------------------
	// 🔥 LOGIC TINH CHỈNH MESSAGE (Kiểm tra nick hoàn thành)
	// -----------------------------------------------------------------------------------------
	if action == "login" || action == "auto" {
		completedIndices := cacheData.StatusMap[STATUS_READ.COMPLETED]
		hasCompletedNick := false
		
		for _, idx := range completedIndices {
			if idx < rawLen {
				if cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
					hasCompletedNick = true
					break
				}
			}
		}
		
		STATE.SheetMutex.RUnlock() // 🔓 Xong việc, mở khóa
		
		if hasCompletedNick {
			return nil, fmt.Errorf("Các tài khoản đã hoàn thành")
		}
	} else {
		STATE.SheetMutex.RUnlock() // 🔓 Xong việc, mở khóa
	}

	return nil, fmt.Errorf("Không còn tài khoản phù hợp")
}

// Xây dựng danh sách ưu tiên chuẩn (Allocation optimized)
func buildPrioritySteps(action string, isReset bool) []PriorityStep {
	// [TỐI ƯU] Ước lượng capacity để tránh re-allocate slice
	steps := make([]PriorityStep, 0, 10)
	
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	if strings.Contains(action, "login") {
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		if isReset {
			add(STATUS_READ.COMPLETED, true, false, 5)
		}
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
		if isReset {
			add(STATUS_READ.COMPLETED, true, false, 9)
		}
	}
	return steps
}

func determineType(row []string) string {
	st := row[INDEX_DATA_TIKTOK.STATUS]
	if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG { return "register" }
	return "login"
}

// Lấy danh sách index cần dọn dẹp
func getCleanupIndices(cache *SheetCacheData, deviceId string, targetIdx int, isResetCompleted bool) []int {
	var list []int
	checkList := []string{STATUS_READ.RUNNING, STATUS_READ.REGISTERING}
	if isResetCompleted {
		checkList = append(checkList, STATUS_READ.COMPLETED)
	}

	for _, st := range checkList {
		indices := cache.StatusMap[st]
		for _, idx := range indices {
			// Bỏ qua nick đang được chọn
			if idx != targetIdx && idx < len(cache.CleanValues) {
				if cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
					list = append(list, idx)
				}
			}
		}
	}
	return list
}

// Hàm commit dữ liệu và trả về response
func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string, priority int) (*LoginResponse, error) {
	// [LOGIC] View Only -> Trả về ngay, không ghi RAM/Disk
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
	// Priority 5 (Login) hoặc 9 (Auto) là Reset Completed
	if (action == "auto" || action == "login_reset") && (priority == 5 || priority == 9) {
		mode = "reset"
		isResetCompleted = true
	}
	
	// Tạo Note mới (đã fix logic tăng Count)
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	// 🔒 KHÓA GHI ĐỂ UPDATE DATA
	STATE.SheetMutex.Lock()
	
	// 1. Dọn dẹp nick cũ (Single Instance Rule)
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
		
		// Update CleanValues (Lowercase)
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = CleanString(cSt) }
		if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = CleanString(cNote) }
		
		// Move Status Map
		if oldCSt != CleanString(cSt) {
			removeFromStatusMap(cache.StatusMap, oldCSt, cIdx)
			newCSt := CleanString(cSt)
			cache.StatusMap[newCSt] = append(cache.StatusMap[newCSt], cIdx)
		}

		// Đẩy vào Queue (Chạy ngầm)
		cRow := make([]interface{}, len(cache.RawValues[cIdx]))
		copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// 2. Update Nick Mục Tiêu (Target)
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
	STATE.SheetMutex.Unlock() // 🔓 Mở khóa GHI

	// Queue Update Target
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
				// Xóa phần tử i (swap element cuối lên để xóa nhanh O(1) hoặc append slice)
				// Với slice nhỏ thì append slice cũng rất nhanh và giữ thứ tự (nếu cần)
				m[status] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// [SELF HEALING] Cập nhật ngay lập tức nick lỗi vào RAM để chặn các request sau
func doSelfHealing(sid string, idx int, missing string, cache *SheetCacheData) {
	msg := "Nick thiếu " + missing + "\n" + time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	
	STATE.SheetMutex.Lock()
	if idx < len(cache.RawValues) {
		cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = STATUS_WRITE.ATTENTION
		cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = msg
		
		if idx < len(cache.CleanValues) && INDEX_DATA_TIKTOK.STATUS < len(cache.CleanValues[idx]) {
			oldSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
			removeFromStatusMap(cache.StatusMap, oldSt, idx)
			// Không cần thêm vào map Attention vì hệ thống ít khi tìm kiếm theo trạng thái này
			cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(STATUS_WRITE.ATTENTION)
		}
	}
	
	fullRow := make([]interface{}, len(cache.RawValues[idx]))
	copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()

	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

// [HELPER] Kiểm tra chất lượng nick
type QualityResult struct { Valid bool; SystemEmail string; Missing string }
func kiem_tra_chat_luong_clean(cleanRow []string, action string) QualityResult {
	if len(cleanRow) <= INDEX_DATA_TIKTOK.EMAIL { return QualityResult{false, "", "data_length"} }
	rawEmail := cleanRow[INDEX_DATA_TIKTOK.EMAIL]
	sysEmail := ""
	if strings.Contains(rawEmail, "@") { parts := strings.Split(rawEmail, "@"); if len(parts) > 1 { sysEmail = parts[1] } }
	if action == "view_only" { return QualityResult{true, sysEmail, ""} }
	
	hasEmail := (rawEmail != "")
	hasUser := (cleanRow[INDEX_DATA_TIKTOK.USER_NAME] != "")
	hasPass := (cleanRow[INDEX_DATA_TIKTOK.PASSWORD] != "")

	if strings.Contains(action, "register") { if hasEmail { return QualityResult{true, sysEmail, ""} }; return QualityResult{false, "", "email"} }
	if strings.Contains(action, "login") { if (hasEmail || hasUser) && hasPass { return QualityResult{true, sysEmail, ""} }; return QualityResult{false, "", "user/pass"} }
	if action == "auto" { if hasEmail || ((hasUser || hasEmail) && hasPass) { return QualityResult{true, sysEmail, ""} }; return QualityResult{false, "", "data"} }
	return QualityResult{false, "", "unknown"}
}

// [TỐI ƯU] Tạo Note sử dụng String Concatenation thay vì Sprintf (Nhanh hơn)
func tao_ghi_chu_chuan(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" {
		return newStatus + "\n" + nowFull
	}
	
	count := 0
	oldNote = strings.TrimSpace(oldNote)
	lines := strings.Split(oldNote, "\n")
	
	// Tìm (Lần x)
	if idx := strings.Index(oldNote, "(Lần"); idx != -1 {
		end := strings.Index(oldNote[idx:], ")")
		if end != -1 {
			// Parse thủ công để tránh overhead của Sscanf
			numStr := oldNote[idx+len("(Lần") : idx+end]
			if c, err := strconv.Atoi(strings.TrimSpace(numStr)); err == nil {
				count = c
			}
		}
	}
	if count == 0 { count = 1 }

	// Logic tăng đếm theo ngày
	today := nowFull[:10]
	oldDate := ""
	for _, l := range lines {
		// Check nhanh có chứa "/" và độ dài >= 10 (dd/mm/yyyy)
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
	
	// Sử dụng cộng chuỗi & strconv.Itoa (Hiệu năng cao hơn Sprintf)
	return st + "\n" + nowFull + " (Lần " + strconv.Itoa(count) + ")"
}
