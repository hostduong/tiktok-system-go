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

func init() {}

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi Body JSON"}`, 400)
		return
	}

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		http.Error(w, `{"status":"false","messenger":"Lỗi xác thực"}`, 401)
		return
	}

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	reqAction := CleanString(body["action"])

	action := "login"
	if reqType == "view" {
		action = "view_only"
	} else if reqType == "auto" {
		action = "auto"
		if reqAction == "reset" {
			body["is_reset"] = true
		}
	} else if reqType == "register" {
		action = "register"
	} else if reqAction == "reset" {
		action = "login_reset"
	}

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
	// Nạp dữ liệu (Đã phân vùng sẵn)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}

	var targetIndex = -1
	var responseType = "login"
	var sysEmail = ""

	// --- 0. PARSE ĐIỀU KIỆN TÌM KIẾM (ĐƯA LÊN ĐẦU) ---
	// Chỉ dùng search_col_x, bỏ hoàn toàn search_user_id/email cũ
	searchCols := make(map[int]string)
	for k, v := range body {
		if strings.HasPrefix(k, "search_col_") {
			if i, err := strconv.Atoi(strings.TrimPrefix(k, "search_col_")); err == nil {
				searchCols[i] = CleanString(v)
			}
		}
	}
	hasSearch := len(searchCols) > 0

	// --- 1. KIỂM TRA NICK ĐANG SỞ HỮU (AssignedMap - O(1)) ---
	if idx, ok := cacheData.AssignedMap[deviceId]; ok {
		if idx < len(cacheData.RawValues) {
			cleanRow := cacheData.CleanValues[idx]
			// Double check DeviceID (đề phòng cache cũ chưa kịp xóa)
			if cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
				
				// Nếu có yêu cầu Search -> Kiểm tra xem nick cũ có khớp Search không?
				isMatchSearch := true
				if hasSearch {
					for colIdx, val := range searchCols {
						cellVal := ""
						if colIdx < len(cleanRow) {
							cellVal = cleanRow[colIdx]
						}
						if cellVal != val {
							isMatchSearch = false
							break
						}
					}
				}

				// Nếu khớp Search (hoặc không search) + Chất lượng OK -> Lấy lại nick cũ
				if isMatchSearch {
					val := kiem_tra_chat_luong_clean(cleanRow, action)
					if val.Valid {
						targetIndex = idx
						sysEmail = val.SystemEmail
						st := cleanRow[INDEX_DATA_TIKTOK.STATUS]
						if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG {
							responseType = "register"
						}
						// Fast return
						return commit_and_response(sid, deviceId, cacheData, targetIndex, responseType, sysEmail, action)
					}
				}
				// Nếu không khớp -> Reset targetIndex, đi xuống dưới tìm nick mới
				targetIndex = -1
			}
		}
	}

	// --- 2. TÌM KIẾM MỚI (NẾU CÓ SEARCH_COL) ---
	if hasSearch {
		// Quét mảng (Bắt buộc O(N) vì search động)
		for i, row := range cacheData.CleanValues {
			match := true
			for colIdx, val := range searchCols {
				cellVal := ""
				if colIdx < len(row) {
					cellVal = row[colIdx]
				}
				if cellVal != val {
					match = false
					break
				}
			}
			if match {
				val := kiem_tra_chat_luong_clean(row, action)
				if val.Valid {
					// Logic mới: Search là phải chiếm quyền (kể cả nick đang của người khác - tùy logic tool)
					// Ở đây giữ logic an toàn: Chỉ lấy nếu nick đó chưa có chủ hoặc chủ là chính mình
					curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
					if curDev == "" || curDev == deviceId {
						targetIndex = i
						sysEmail = val.SystemEmail
						st := row[INDEX_DATA_TIKTOK.STATUS]
						if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING {
							responseType = "register"
						}
						return commit_and_response(sid, deviceId, cacheData, targetIndex, responseType, sysEmail, action)
					}
				}
			}
		}
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo yêu cầu")
	}

	// --- 3. AUTO PICK (LẤY NICK TRỐNG TỪ STATUS MAP) ---
	if targetIndex == -1 && action != "view_only" {
		isReset := false
		if v, ok := body["is_reset"].(bool); ok && v {
			isReset = true
		}

		priorities := getPriorityList(action, isReset)

		for _, statusKey := range priorities {
			candidateIndices := cacheData.StatusMap[statusKey] // List O(1)
			
			for _, idx := range candidateIndices {
				// Kiểm tra DeviceID == "" (Chỉ lấy nick trống)
				currentDev := ""
				if idx < len(cacheData.CleanValues) {
					// Dùng CleanValues truy cập nhanh
					if INDEX_DATA_TIKTOK.DEVICE_ID < len(cacheData.CleanValues[idx]) {
						currentDev = cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
					}
				}

				if currentDev == "" {
					val := kiem_tra_chat_luong_clean(cacheData.CleanValues[idx], action)
					if val.Valid {
						// 🔥 LOCK & CHECK
						STATE.SheetMutex.Lock()
						
						// Double Check trong Lock
						dCheck := ""
						if idx < len(cacheData.CleanValues) {
							dCheck = cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
						}

						if dCheck == "" {
							// CHIẾM QUYỀN
							cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							
							// Cập nhật AssignedMap
							cacheData.AssignedMap[deviceId] = idx
							
							// (Nếu muốn tối ưu hơn: Xóa khỏi UnassignedList ở đây - nhưng tạm thời chưa cần thiết vì ta check dCheck == "")

							targetIndex = idx
							sysEmail = val.SystemEmail
							if statusKey == STATUS_READ.REGISTERING || statusKey == STATUS_READ.WAIT_REG || statusKey == STATUS_READ.REGISTER {
								responseType = "register"
							}
							
							STATE.SheetMutex.Unlock()
							goto FOUND
						}
						STATE.SheetMutex.Unlock()
					}
				}
			}
		}
	}

FOUND:
	if targetIndex == -1 {
		return nil, fmt.Errorf("Không còn tài khoản phù hợp")
	}

	return commit_and_response(sid, deviceId, cacheData, targetIndex, responseType, sysEmail, action)
}

// 🟢 HELPER: Commit Data & Build Response
func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string) (*LoginResponse, error) {
	row := cache.RawValues[idx]
	
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" {
		tSt = STATUS_WRITE.REGISTERING
	}

	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	cleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	if cleanSt == STATUS_READ.COMPLETED {
		mode = "reset"
	}
	
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	// Update RAM
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
	
	// Update StatusMap
	if cleanSt != "" {
		removeFromStatusMap(cache.StatusMap, cleanSt, idx)
	}
	newCleanSt := CleanString(tSt)
	cache.StatusMap[newCleanSt] = append(cache.StatusMap[newCleanSt], idx)
	
	STATE.SheetMutex.Unlock()

	// Update Queue
	newRow := make([]interface{}, len(row))
	copy(newRow, row)
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	msg := "Lấy nick đăng nhập thành công"
	if typ == "register" {
		msg = "Lấy nick đăng ký thành công"
	}

	return &LoginResponse{
		Status:          "true",
		Type:            typ,
		Messenger:       msg,
		DeviceId:        deviceId,
		RowIndex:        RANGES.DATA_START_ROW + idx,
		SystemEmail:     email,
		AuthProfile:     MakeAuthProfile(newRow),
		ActivityProfile: MakeActivityProfile(newRow),
		AiProfile:       MakeAiProfile(newRow),
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

func getPriorityList(action string, isReset bool) []string {
	var list []string
	if strings.Contains(action, "login") {
		list = append(list, STATUS_READ.RUNNING, STATUS_READ.WAITING, STATUS_READ.LOGIN)
	} else if action == "register" {
		list = append(list, STATUS_READ.REGISTERING, STATUS_READ.WAIT_REG, STATUS_READ.REGISTER)
	} else if action == "auto" {
		list = append(list, STATUS_READ.RUNNING, STATUS_READ.WAITING, STATUS_READ.LOGIN)
		list = append(list, STATUS_READ.REGISTERING, STATUS_READ.WAIT_REG, STATUS_READ.REGISTER)
	}
	if isReset {
		list = append(list, STATUS_READ.COMPLETED)
	}
	return list
}

type QualityResult struct {
	Valid       bool
	SystemEmail string
	Missing     string
}

func kiem_tra_chat_luong_clean(cleanRow []string, action string) QualityResult {
	if len(cleanRow) <= INDEX_DATA_TIKTOK.EMAIL {
		return QualityResult{false, "", "data_length"}
	}
	rawEmail := cleanRow[INDEX_DATA_TIKTOK.EMAIL]
	sysEmail := ""
	if strings.Contains(rawEmail, "@") {
		parts := strings.Split(rawEmail, "@")
		if len(parts) > 1 { sysEmail = parts[1] }
	}
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
	if mode == "new" {
		if newStatus == "" { newStatus = "Đang chờ" }
		return fmt.Sprintf("%s\n%s", newStatus, nowFull)
	}
	count := 0
	oldNote = strings.TrimSpace(oldNote)
	lines := strings.Split(oldNote, "\n")
	lastLine := ""
	if len(lines) > 0 { lastLine = lines[len(lines)-1] }
	if idx := strings.Index(lastLine, "(Lần"); idx != -1 {
		endIdx := strings.Index(lastLine[idx:], ")")
		if endIdx != -1 {
			numStr := lastLine[idx+len("(Lần") : idx+endIdx]
			c, _ := strconv.Atoi(strings.TrimSpace(numStr))
			count = c
		}
	}
	if count == 0 { count = 1 }
	if mode == "updated" {
		statusToUse := newStatus
		if statusToUse == "" && len(lines) > 0 { statusToUse = lines[0] }
		if statusToUse == "" { statusToUse = "Đang chạy" }
		return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
	}
	todayStr := nowFull[:10]
	oldDate := ""
	if len(lines) >= 2 {
		for _, l := range lines {
			if strings.Contains(l, "/") && len(l) >= 10 {
				oldDate = l[:10]
				break
			}
		}
	}
	if oldDate != todayStr {
		count = 1
	} else {
		if mode == "reset" { count++ } else if count == 0 { count = 1 }
	}
	return fmt.Sprintf("%s\n%s (Lần %d)", newStatus, nowFull, count)
}
