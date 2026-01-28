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

// Handler chính cho: login, register, auto, view, reset
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

	spreadsheetId := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	reqAction := CleanString(body["action"])

	// Logic map action chuẩn Node.js
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

	body["action"] = action

	res, err := xu_ly_lay_du_lieu(spreadsheetId, deviceId, body, action)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}

	allData := cacheData.RawValues
	cleanValues := cacheData.CleanValues
	
	targetIndex := -1
	targetData := make([]interface{}, 61)
	responseType := "login"
	sysEmail := ""
	
	var cleanupIndices []int
	var badIndices []map[string]interface{}

	// 🔥 FIX: Logic đọc row_index thông minh (Hỗ trợ cả String và Int/Float)
	reqRowIndex := -1
	if v, ok := body["row_index"]; ok {
		switch val := v.(type) {
		case float64:
			reqRowIndex = int(val)
		case string:
			if val != "" {
				if i, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
					reqRowIndex = i
				}
			}
		}
	}
	
	// --- 1. FAST MODE ---
	isFast := false
	if reqRowIndex >= RANGES.DATA_START_ROW {
		idx := reqRowIndex - RANGES.DATA_START_ROW
		
		if idx >= 0 && idx < len(allData) {
			clean := cleanValues[idx]
			s_uid := CleanString(body["search_user_id"])
			
			match := (s_uid == "") || (clean[INDEX_DATA_TIKTOK.USER_ID] == s_uid)
			
			if match {
				val := kiem_tra_chat_luong_clean(clean, action)
				
				if val.Valid {
					targetIndex = idx
					targetData = allData[idx]
					isFast = true
					sysEmail = val.SystemEmail
					
					st := clean[INDEX_DATA_TIKTOK.STATUS]
					if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG {
						responseType = "register"
					} else {
						responseType = "login"
					}
					
					cleanupIndices = lay_danh_sach_cleanup(cleanValues, cacheData.Indices, deviceId, false, idx)
				} else if action != "view_only" {
					badIndices = append(badIndices, map[string]interface{}{
						"index": idx, "msg": "Thiếu " + val.Missing,
					})
				}
			}
		}
	}

	// --- 2. AUTO SEARCH MODE ---
	prio := 0
	if !isFast {
		searchRes := xu_ly_tim_kiem(body, action, deviceId, cacheData, sid)
		
		targetIndex = searchRes.TargetIndex
		responseType = searchRes.ResponseType
		sysEmail = searchRes.SystemEmail
		cleanupIndices = searchRes.CleanupIndices
		prio = searchRes.BestPriority
		
		if len(searchRes.BadIndices) > 0 {
			badIndices = append(badIndices, searchRes.BadIndices...)
		}

		if targetIndex == -1 {
			if action != "view_only" && len(badIndices) > 0 {
				xu_ly_ghi_loi(sid, badIndices)
			}
			return nil, fmt.Errorf("Không còn tài khoản phù hợp")
		}
		
		targetData = allData[targetIndex]
	}

	// --- 3. VIEW ONLY ---
	if action == "view_only" {
		return buildResponse(targetData, targetIndex, responseType, "OK", deviceId, sysEmail), nil
	}

	// --- 4. CHECK & WRITE ---
	curDev := CleanString(targetData[INDEX_DATA_TIKTOK.DEVICE_ID])
	if curDev != deviceId && curDev != "" {
		return nil, fmt.Errorf("Hệ thống bận (Nick vừa bị người khác lấy).")
	}

	tSt := STATUS_WRITE.RUNNING
	if responseType == "register" {
		tSt = STATUS_WRITE.REGISTERING
	}

	oldNote := SafeString(targetData[INDEX_DATA_TIKTOK.NOTE])
	isResetAction := (prio == 5 || prio == 9)
	mode := "normal"
	if isResetAction { mode = "reset" }
	
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	newRow := make([]interface{}, len(targetData))
	copy(newRow, targetData)
	
	newRow[INDEX_DATA_TIKTOK.STATUS] = tSt
	newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	newRow[INDEX_DATA_TIKTOK.NOTE] = tNote

	// Cập nhật RAM
	STATE.SheetMutex.Lock()
	cacheKey := sid + KEY_SEPARATOR + SHEET_NAMES.DATA_TIKTOK
	if c, ok := STATE.SheetCache[cacheKey]; ok {
		c.RawValues[targetIndex][INDEX_DATA_TIKTOK.STATUS] = tSt
		c.RawValues[targetIndex][INDEX_DATA_TIKTOK.NOTE] = tNote
		c.RawValues[targetIndex][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
		
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
			c.CleanValues[targetIndex][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt)
		}
		if INDEX_DATA_TIKTOK.DEVICE_ID < CACHE.CLEAN_COL_LIMIT {
			c.CleanValues[targetIndex][INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(deviceId)
		}
	}
	STATE.SheetMutex.Unlock()

	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, targetIndex, newRow)

	if len(cleanupIndices) > 0 {
		cSt := STATUS_WRITE.WAITING
		if responseType == "register" {
			cSt = STATUS_WRITE.WAIT_REG
		}
		
		for _, i := range cleanupIndices {
			if i == targetIndex { continue }
			
			oldN := SafeString(allData[i][INDEX_DATA_TIKTOK.NOTE])
			cNote := ""
			if isResetAction {
				cNote = tao_ghi_chu_chuan(oldN, "Reset chờ chạy", "reset")
			}
			
			cRow := make([]interface{}, len(allData[i]))
			copy(cRow, allData[i])
			cRow[INDEX_DATA_TIKTOK.STATUS] = cSt
			cRow[INDEX_DATA_TIKTOK.NOTE] = cNote
			
			QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, i, cRow)
		}
	}

	if len(badIndices) > 0 {
		xu_ly_ghi_loi(sid, badIndices)
	}

	msg := "Lấy nick đăng nhập thành công"
	if responseType == "register" {
		msg = "Lấy nick đăng ký thành công"
	}

	return buildResponse(newRow, targetIndex, responseType, msg, deviceId, sysEmail), nil
}

// Logic Search, Quality, GroupConfig... (Giữ nguyên như bản chuẩn)
type SearchResult struct {
	TargetIndex    int
	ResponseType   string
	SystemEmail    string
	BestPriority   int
	CleanupIndices []int
	BadIndices     []map[string]interface{}
}

type QualityResult struct {
	Valid       bool
	SystemEmail string
	Missing     string
}

type GroupConfig struct {
	Indices  []int
	Type     string
	Priority int
	IsMy     bool
}

func xu_ly_tim_kiem(body map[string]interface{}, action, reqDevice string, cacheData *SheetCacheData, sid string) SearchResult {
	cleanValues := cacheData.CleanValues
	indices := cacheData.Indices
	
	s_uid := CleanString(body["search_user_id"])
	s_email := CleanString(body["search_email"])
	isSearchMode := (s_uid != "" || s_email != "")
	
	isReset := (action == "login_reset")
	if val, ok := body["is_reset"].(bool); ok && val {
		isReset = true
	}

	if isSearchMode {
		idx := -1
		if s_uid != "" {
			if i, ok := indices["userId"][s_uid]; ok { idx = i }
		} else if s_email != "" {
			if i, ok := indices["email"][s_email]; ok { idx = i }
		}

		if idx != -1 {
			st := cleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
			typ := "login"
			if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING {
				typ = "register"
			}
			val := kiem_tra_chat_luong_clean(cleanValues[idx], typ)
			if val.Valid {
				return SearchResult{
					TargetIndex:    idx,
					ResponseType:   typ,
					SystemEmail:    val.SystemEmail,
					CleanupIndices: lay_danh_sach_cleanup(cleanValues, indices, reqDevice, false, idx),
				}
			} else {
				return SearchResult{TargetIndex: -1, BadIndices: []map[string]interface{}{{"index": idx, "msg": "Thiếu " + val.Missing}}}
			}
		}
		return SearchResult{TargetIndex: -1}
	}

	var groups []GroupConfig
	getIdx := func(st string) []int {
		if list, ok := cacheData.StatusIndices[st]; ok { return list }
		return []int{}
	}

	completedIndices := getIdx(STATUS_READ.COMPLETED)

	if strings.Contains(action, "login") {
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.RUNNING), "login", 1, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.WAITING), "login", 2, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.LOGIN), "login", 3, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.LOGIN), "login", 4, false})
		if isReset {
			groups = append(groups, GroupConfig{completedIndices, "login", 5, true})
		}
	} else if action == "register" {
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.REGISTERING), "register", 1, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.WAIT_REG), "register", 2, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.REGISTER), "register", 3, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.REGISTER), "register", 4, false})
	} else if action == "auto" {
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.RUNNING), "login", 1, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.WAITING), "login", 2, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.LOGIN), "login", 3, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.LOGIN), "login", 4, false})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.REGISTERING), "register", 5, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.WAIT_REG), "register", 6, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.REGISTER), "register", 7, true})
		groups = append(groups, GroupConfig{getIdx(STATUS_READ.REGISTER), "register", 8, false})
		if isReset {
			groups = append(groups, GroupConfig{completedIndices, "login", 9, true})
		}
	}

	bestIndex := -1
	bestPriority := 999
	bestType := "login"
	bestSystemEmail := ""
	var badIndices []map[string]interface{}

	for _, g := range groups {
		if g.Priority >= bestPriority { continue }
		for _, i := range g.Indices {
			row := cleanValues[i]
			curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
			isMy := (curDev == reqDevice)
			isNoDev := (curDev == "")

			if (g.IsMy && isMy) || (!g.IsMy && isNoDev) {
				val := kiem_tra_chat_luong_clean(row, g.Type)
				if !val.Valid {
					badIndices = append(badIndices, map[string]interface{}{"index": i, "msg": "Thiếu " + val.Missing})
					continue
				}

				if isMy {
					bestIndex = i
					bestPriority = g.Priority
					bestType = g.Type
					bestSystemEmail = val.SystemEmail
					break
				} else if isNoDev {
					STATE.SheetMutex.Lock()
					if cacheData.CleanValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] == "" {
						cacheData.CleanValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = reqDevice
						cacheData.RawValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = reqDevice
						bestIndex = i
						bestPriority = g.Priority
						bestType = g.Type
						bestSystemEmail = val.SystemEmail
						STATE.SheetMutex.Unlock()
						break
					}
					STATE.SheetMutex.Unlock()
				}
			}
		}
		if bestIndex != -1 { break }
	}

	cleanupIndices := []int{}
	if bestIndex != -1 {
		isResetCompleted := (bestPriority == 5 || bestPriority == 9)
		cleanupIndices = lay_danh_sach_cleanup(cleanValues, cacheData.Indices, reqDevice, isResetCompleted, bestIndex)
	}

	return SearchResult{TargetIndex: bestIndex, ResponseType: bestType, SystemEmail: bestSystemEmail, BestPriority: bestPriority, CleanupIndices: cleanupIndices, BadIndices: badIndices}
}

func kiem_tra_chat_luong_clean(cleanRow []string, action string) QualityResult {
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

func lay_danh_sach_cleanup(cleanValues [][]string, indices map[string]map[string]int, reqDevice string, isReset bool, target int) []int {
	list := make([]int, 0)
	checkSt := []string{STATUS_READ.RUNNING, STATUS_READ.REGISTERING}
	if isReset {
		checkSt = append(checkSt, STATUS_READ.COMPLETED)
	}
	for i, row := range cleanValues {
		if i == target { continue }
		if row[INDEX_DATA_TIKTOK.DEVICE_ID] == reqDevice {
			st := row[INDEX_DATA_TIKTOK.STATUS]
			for _, c := range checkSt {
				if st == c {
					list = append(list, i)
					break
				}
			}
		}
	}
	return list
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

func xu_ly_ghi_loi(sid string, badIndices []map[string]interface{}) {
	for _, item := range badIndices {
		idx := item["index"].(int)
		msg := item["msg"].(string)
		fmt.Printf("⚠️ [BAD NICK] Index %d: %s\n", idx, msg)
	}
}

func buildResponse(row []interface{}, idx int, typ, msg, devId, email string) *LoginResponse {
	return &LoginResponse{
		Status:          "true",
		Type:            typ,
		Messenger:       msg,
		DeviceId:        devId,
		RowIndex:        RANGES.DATA_START_ROW + idx,
		SystemEmail:     email,
		AuthProfile:     MakeAuthProfile(row),
		ActivityProfile: MakeActivityProfile(row),
		AiProfile:       MakeAiProfile(row),
	}
}
