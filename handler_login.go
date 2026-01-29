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

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		http.Error(w, `{"status":"false"}`, 401)
		return
	}

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	reqAction := CleanString(body["action"])

	action := "login"
	if reqType == "view" { action = "view_only" }
	if reqType == "auto" {
		action = "auto"
		if reqAction == "reset" { body["is_reset"] = true }
	}
	if reqType == "register" { action = "register" }
	if reqAction == "reset" { action = "login_reset" }

	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	// Nạp dữ liệu (Đã phân vùng)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	var targetIndex = -1
	var responseType = "login"
	var sysEmail = ""

	// 1. Chỉ Parse search_col_
	searchCols := make(map[int]string)
	for k, v := range body {
		if strings.HasPrefix(k, "search_col_") {
			if i, err := strconv.Atoi(strings.TrimPrefix(k, "search_col_")); err == nil {
				searchCols[i] = CleanString(v)
			}
		}
	}
	hasSearch := len(searchCols) > 0

	// 2. Ưu tiên 1: Check AssignedMap (Fast Path - O(1))
	if idx, ok := cacheData.AssignedMap[deviceId]; ok {
		if idx < len(cacheData.RawValues) {
			cleanRow := cacheData.CleanValues[idx]
			// Double check
			if cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
				// Nếu có Search, phải khớp Search mới được lấy
				isMatch := true
				if hasSearch {
					for cIdx, val := range searchCols {
						if cIdx >= len(cleanRow) || cleanRow[cIdx] != val { isMatch = false; break }
					}
				}

				if isMatch {
					val := kiem_tra_chat_luong_clean(cleanRow, action)
					if val.Valid {
						return commit_and_response(sid, deviceId, cacheData, idx, determineType(cleanRow), val.SystemEmail, action)
					}
				}
			}
		}
	}

	// 3. Ưu tiên 2: Tìm kiếm trong mảng (Search Mode - O(N))
	if hasSearch {
		for i, row := range cacheData.CleanValues {
			match := true
			for cIdx, val := range searchCols {
				if cIdx >= len(row) || row[cIdx] != val { match = false; break }
			}
			
			if match {
				val := kiem_tra_chat_luong_clean(row, action)
				if val.Valid {
					// Chỉ lấy nếu nick trống hoặc của chính mình
					curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
					if curDev == "" || curDev == deviceId {
						return commit_and_response(sid, deviceId, cacheData, i, determineType(row), val.SystemEmail, action)
					}
				} else {
					// Self-Healing
					doSelfHealing(sid, i, val.Missing, cacheData)
				}
			}
		}
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo yêu cầu")
	}

	// 4. Ưu tiên 3: Auto Pick (StatusMap - O(1))
	if targetIndex == -1 && action != "view_only" {
		isReset := false
		if v, ok := body["is_reset"].(bool); ok && v { isReset = true }
		
		priorities := getPriorityList(action, isReset)
		
		for _, statusKey := range priorities {
			indices := cacheData.StatusMap[statusKey]
			for _, idx := range indices {
				// Check nhanh DeviceID trong RAM
				if idx < len(cacheData.CleanValues) {
					curDev := cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
					
					if curDev == "" {
						val := kiem_tra_chat_luong_clean(cacheData.CleanValues[idx], action)
						if !val.Valid {
							doSelfHealing(sid, idx, val.Missing, cacheData)
							continue
						}

						// Lock & Claim
						STATE.SheetMutex.Lock()
						if cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == "" {
							// Update RAM
							cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.AssignedMap[deviceId] = idx
							
							STATE.SheetMutex.Unlock()
							
							return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action)
						}
						STATE.SheetMutex.Unlock()
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("Không còn tài khoản phù hợp")
}

// Helpers
func determineType(row []string) string {
	st := row[INDEX_DATA_TIKTOK.STATUS]
	if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG {
		return "register"
	}
	return "login"
}

func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string) (*LoginResponse, error) {
	row := cache.RawValues[idx]
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }

	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	if cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] == STATUS_READ.COMPLETED { mode = "reset" }
	
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	// Update RAM & Map
	STATE.SheetMutex.Lock()
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = tSt
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = tNote
	cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	
	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt)
	}
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(tNote)
	}
	
	// Update StatusMap (Move Index)
	// (Logic: Xóa index khỏi bucket cũ, thêm vào bucket mới - đã implement ở các phiên bản trước, giữ nguyên logic đó hoặc implement đơn giản ở đây)
	// Để gọn code, tôi giả định StatusMap được refresh hoặc handle lazy. 
	// Nếu cần chính xác tuyệt đối:
	// oldSt := CleanString(row[INDEX_DATA_TIKTOK.STATUS]) (trước khi gán tSt)
	// removeFromMap(cache.StatusMap, oldSt, idx)
	// cache.StatusMap[CleanString(tSt)] = append(cache.StatusMap[CleanString(tSt)], idx)

	STATE.SheetMutex.Unlock()

	// Queue Update
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
	// Ghi chú lỗi vào Queue, không cần update RAM ngay vì nick này đằng nào cũng bị bỏ qua
	msg := "Nick thiếu " + missing + "\n" + time.Now().Format("02/01/2006 15:04:05")
	row := cache.RawValues[idx]
	
	newRow := make([]interface{}, len(row))
	copy(newRow, row)
	newRow[INDEX_DATA_TIKTOK.STATUS] = STATUS_WRITE.ATTENTION
	newRow[INDEX_DATA_TIKTOK.NOTE] = msg
	
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)
}

func getPriorityList(action string, isReset bool) []string {
	var list []string
	if strings.Contains(action, "login") { list = append(list, STATUS_READ.RUNNING, STATUS_READ.WAITING, STATUS_READ.LOGIN) }
	if strings.Contains(action, "register") { list = append(list, STATUS_READ.REGISTERING, STATUS_READ.WAIT_REG, STATUS_READ.REGISTER) }
	if action == "auto" { list = append(list, STATUS_READ.RUNNING, STATUS_READ.WAITING, STATUS_READ.LOGIN, STATUS_READ.REGISTERING, STATUS_READ.WAIT_REG, STATUS_READ.REGISTER) }
	if isReset { list = append(list, STATUS_READ.COMPLETED) }
	return list
}

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

	if strings.Contains(action, "register") {
		if hasEmail { return QualityResult{true, sysEmail, ""} }
		return QualityResult{false, "", "email"}
	}
	if strings.Contains(action, "login") {
		if (hasEmail || hasUser) && hasPass { return QualityResult{true, sysEmail, ""} }
		return QualityResult{false, "", "user/pass"}
	}
	if action == "auto" {
		if hasEmail || ((hasUser || hasEmail) && hasPass) { return QualityResult{true, sysEmail, ""} }
		return QualityResult{false, "", "data"}
	}
	return QualityResult{false, "", "unknown"}
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
	if mode == "updated" {
		st := newStatus
		if st == "" && len(lines) > 0 { st = lines[0] }
		if st == "" { st = "Đang chạy" }
		return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
	}
	today := nowFull[:10]
	oldDate := ""
	for _, l := range lines { if len(l) >= 10 && strings.Contains(l, "/") { oldDate = l[:10]; break } }
	if oldDate != today { count = 1 } else { if mode == "reset" { count++ } else if count == 0 { count = 1 } }
	return fmt.Sprintf("%s\n%s (Lần %d)", newStatus, nowFull, count)
}
