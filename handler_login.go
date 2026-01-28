package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Request Body chuẩn cho Login
type LoginRequest struct {
	Token          string `json:"token"`
	Type           string `json:"type"`
	Action         string `json:"action"`
	DeviceID       string `json:"deviceId"`
	RowIndex       string `json:"row_index"`
	SearchUserID   string `json:"search_user_id"`
	SearchUserSec  string `json:"search_user_sec"`
	SearchUserName string `json:"search_user_name"`
	SearchEmail    string `json:"search_email"`
	IsReset        bool   `json:"is_reset"` // Cho action=reset
}

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// 1. Parse Body
	var body LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// 2. Check Auth [cite: 193-210]
	auth := CheckToken(body.Token)
	if !auth.IsValid {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": auth.Messenger})
		return
	}

	sid := auth.SpreadsheetID
	did := CleanString(body.DeviceID)
	
	// Validate DeviceID
	if did == "" && body.Type != "view" {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Thiếu deviceId"})
		return
	}

	// 3. Chuẩn hóa Action [cite: 446-447]
	action := CleanString(body.Action)
	if body.Type == "view" {
		action = "view_only"
	} else if body.Type == "auto" {
		action = "auto"
		if body.Action == "reset" {
			body.IsReset = true
		}
	} else if body.Type == "register" {
		action = "register"
	} else if body.Action == "reset" {
		action = "login_reset"
	} else {
		action = "login" // Default
	}

	// 4. Load Data
	cache, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "error", "messenger": "Lỗi tải dữ liệu"})
		return
	}

	// 5. SEARCH LOGIC [cite: 217-255]
	targetIndex := -1
	targetData := make([]interface{}, 0)
	sysEmail := ""
	responseType := "login"
	cleanupIndices := []int{}
	priority := 999
	
	isReadOnly := (action == "view_only")
	
	// --- A. TÌM KIẾM ĐÍCH DANH (SEARCH MODE) ---
	sUID := CleanString(body.SearchUserID)
	sSec := CleanString(body.SearchUserSec)
	sName := CleanString(body.SearchUserName)
	sEmail := CleanString(body.SearchEmail)
	rowIndexInput, _ := strconv.Atoi(body.RowIndex)

	if rowIndexInput >= RANGES.DATA_START_ROW || sUID != "" || sSec != "" || sName != "" || sEmail != "" {
		idx := -1
		
		// Tìm theo Index Map O(1)
		cache.Mutex.RLock()
		if rowIndexInput >= RANGES.DATA_START_ROW {
			ramIdx := rowIndexInput - RANGES.DATA_START_ROW
			if ramIdx < len(cache.CleanValues) { idx = ramIdx }
		} else if sUID != "" {
			if i, ok := cache.Indices["userId"][sUID]; ok { idx = i }
		} else if sSec != "" {
			if i, ok := cache.Indices["userSec"][sSec]; ok { idx = i }
		} else if sName != "" {
			if i, ok := cache.Indices["userName"][sName]; ok { idx = i }
		} else if sEmail != "" {
			if i, ok := cache.Indices["email"][sEmail]; ok { idx = i }
		}
		
		if idx != -1 {
			cleanRow := cache.CleanValues[idx]
			rawRow := cache.RawValues[idx]
			st := cleanRow[INDEX_DATA_TIKTOK.STATUS]
			
			responseType = "login"
			if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG {
				responseType = "register"
			}
			
			// Validate chất lượng nick
			val := checkQuality(cleanRow, action)
			if val.Valid {
				targetIndex = idx
				targetData = rawRow 
				sysEmail = val.SystemEmail
				cleanupIndices = findCleanupIndices(cache, did, false, idx)
			}
		}
		cache.Mutex.RUnlock()
	}

	// --- B. TÌM KIẾM TỰ ĐỘNG (AUTO MODE) ---
	if targetIndex == -1 && !isReadOnly {
		// Định nghĩa các nhóm ưu tiên [cite: 232-235]
		groups := definePriorityGroups(action, body.IsReset)
		
		for _, g := range groups {
			if g.Priority >= priority { continue }
			
			cache.Mutex.RLock()
			candidateIndices := cache.StatusIndices[g.Status]
			cache.Mutex.RUnlock()

			for _, idx := range candidateIndices {
				cache.Mutex.RLock()
				if idx >= len(cache.CleanValues) { cache.Mutex.RUnlock(); continue }
				cleanRow := cache.CleanValues[idx]
				curDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
				cache.Mutex.RUnlock()

				isMy := (curDev == did)
				isNoDev := (curDev == "")

				if (g.My && isMy) || (!g.My && isNoDev) {
					// Check chất lượng
					val := checkQuality(cleanRow, g.Type)
					if !val.Valid {
						// Mark Error -> Đẩy Queue [cite: 239-242]
						markError(sid, idx, "Nick thiếu "+val.Missing)
						continue
					}

					// 🔥 OPTIMISTIC LOCKING CORE [cite: 242-251]
					if isMy {
						targetIndex = idx; priority = g.Priority; responseType = g.Type; sysEmail = val.SystemEmail
						cache.Mutex.RLock()
						targetData = cache.RawValues[idx]
						cache.Mutex.RUnlock()
						break
					} else if isNoDev {
						cache.Mutex.Lock() // BLOCKING
						if cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == "" {
							cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = did
							cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = did
							
							targetIndex = idx; priority = g.Priority; responseType = g.Type; sysEmail = val.SystemEmail
							targetData = cache.RawValues[idx]
							
							cache.Mutex.Unlock()
							break 
						}
						cache.Mutex.Unlock()
					}
				}
			}
			if targetIndex != -1 { break }
		}
		
		if targetIndex != -1 {
			isResetCompleted := (priority == 5 || priority == 9)
			cache.Mutex.RLock()
			cleanupIndices = findCleanupIndices(cache, did, isResetCompleted, targetIndex)
			cache.Mutex.RUnlock()
		}
	}

	// 6. XỬ LÝ KẾT QUẢ & UPDATE
	if targetIndex == -1 {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Không còn tài khoản phù hợp"})
		return
	}

	excelRow := RANGES.DATA_START_ROW + targetIndex
	
	// Build response profiles - Dùng hàm mapProfile ĐẦY ĐỦ [cite: 80-86]
	authProfile := mapProfile(targetData, 0, 22)
	activityProfile := mapProfile(targetData, 23, 44)
	aiProfile := mapProfile(targetData, 45, 60)

	if isReadOnly {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "true", "type": responseType, "messenger": "OK",
			"deviceId": did, "row_index": excelRow, "system_email": sysEmail,
			"auth_profile": authProfile, "activity_profile": activityProfile, "ai_profile": aiProfile,
		})
		return
	}
	
	// Update Note & Status
	newStatus := STATUS_WRITE.RUNNING
	if responseType == "register" { newStatus = STATUS_WRITE.REGISTERING }
	
	rawNote, _ := targetData[INDEX_DATA_TIKTOK.NOTE].(string)
	isResetAction := (priority == 5 || priority == 9)
	mode := "normal"
	if isResetAction { mode = "reset" }
	
	newNote := CreateStandardNote(rawNote, newStatus, mode)

	// Cập nhật RAM & Queue
	cache.Mutex.Lock()
	cache.RawValues[targetIndex][INDEX_DATA_TIKTOK.STATUS] = newStatus
	cache.RawValues[targetIndex][INDEX_DATA_TIKTOK.NOTE] = newNote
	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[targetIndex][INDEX_DATA_TIKTOK.STATUS] = CleanString(newStatus)
	}
	cache.Mutex.Unlock()

	// Đẩy vào Queue Update
	rowToUpdate := make([]interface{}, len(cache.RawValues[targetIndex]))
	copy(rowToUpdate, cache.RawValues[targetIndex])
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, targetIndex, rowToUpdate)

	// Xử lý Cleanup [cite: 281-286]
	if len(cleanupIndices) > 0 {
		cleanSt := STATUS_WRITE.WAITING
		if responseType == "register" { cleanSt = STATUS_WRITE.WAIT_REG }
		
		for _, cIdx := range cleanupIndices {
			cache.Mutex.Lock()
			cNote, _ := cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE].(string)
			newCNote := ""
			if isResetAction { newCNote = CreateStandardNote(cNote, "Reset chờ chạy", "reset") }
			cache.RawValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = cleanSt
			cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = newCNote
			
			if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
				cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = CleanString(cleanSt)
			}
			
			cRow := make([]interface{}, len(cache.RawValues[cIdx]))
			copy(cRow, cache.RawValues[cIdx])
			cache.Mutex.Unlock()
			
			QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "true", "type": responseType, 
		"messenger": "Lấy nick thành công",
		"deviceId": did, "row_index": excelRow, "system_email": sysEmail,
		"auth_profile": authProfile, "activity_profile": activityProfile, "ai_profile": aiProfile,
	})
}

// --- Helper Functions ---

type QualityResult struct { Valid bool; SystemEmail string; Missing string }

func checkQuality(row []string, action string) QualityResult {
	email := row[INDEX_DATA_TIKTOK.EMAIL]
	sysEmail := ""
	if strings.Contains(email, "@") { parts := strings.Split(email, "@"); if len(parts)>1 { sysEmail = parts[1] } }
	
	hasEmail := (email != "")
	hasUser := (row[INDEX_DATA_TIKTOK.USER_NAME] != "")
	hasPass := (row[INDEX_DATA_TIKTOK.PASSWORD] != "")

	if strings.Contains(action, "register") {
		if hasEmail { return QualityResult{true, sysEmail, ""} }
		return QualityResult{false, "", "email"}
	}
	if (hasEmail || hasUser) && hasPass { return QualityResult{true, sysEmail, ""} }
	if action == "auto" && hasEmail { return QualityResult{true, sysEmail, ""} }
	return QualityResult{false, "", "user/pass"}
}

type PriorityGroup struct { Status string; Type string; Priority int; My bool }

func definePriorityGroups(action string, isReset bool) []PriorityGroup {
	r, w, l, reg, wreg, c := STATUS_READ.RUNNING, STATUS_READ.WAITING, STATUS_READ.LOGIN, STATUS_READ.REGISTER, STATUS_READ.WAIT_REG, STATUS_READ.COMPLETED
	registering := STATUS_READ.REGISTERING

	if strings.Contains(action, "login") {
		list := []PriorityGroup{
			{r, "login", 1, true}, {w, "login", 2, true}, {l, "login", 3, true}, {l, "login", 4, false},
		}
		if isReset { list = append(list, PriorityGroup{c, "login", 5, true}) }
		return list
	}
	if action == "register" {
		return []PriorityGroup{
			{registering, "register", 1, true}, {wreg, "register", 2, true}, {reg, "register", 3, true}, {reg, "register", 4, false},
		}
	}
	list := []PriorityGroup{
		{r, "login", 1, true}, {w, "login", 2, true}, {l, "login", 3, true}, {l, "login", 4, false},
		{registering, "register", 5, true}, {wreg, "register", 6, true}, {reg, "register", 7, true}, {reg, "register", 8, false},
	}
	if isReset { list = append(list, PriorityGroup{c, "login", 9, true}) }
	return list
}

func findCleanupIndices(cache *SheetCacheData, did string, isResetCompleted bool, targetIdx int) []int {
	list := []int{}
	checkSt := []string{STATUS_READ.RUNNING, STATUS_READ.REGISTERING}
	if isResetCompleted { checkSt = append(checkSt, STATUS_READ.COMPLETED) }
	
	for _, st := range checkSt {
		if idxs, ok := cache.StatusIndices[st]; ok {
			for _, i := range idxs {
				if i != targetIdx && i < len(cache.CleanValues) {
					if cache.CleanValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] == did {
						list = append(list, i)
					}
				}
			}
		}
	}
	return list
}

func markError(sid string, idx int, msg string) {
	note := msg + "\n" + GetTimeVN()
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, []interface{}{STATUS_WRITE.ATTENTION, note})
}

// mapProfile: Ánh xạ 100% dữ liệu từ Array sang Map (giống logic Node.js anh_xa_...)
// [cite: 79-86] Node.js duyệt qua keys và lowercase chúng. Ở đây ta hardcode mapping để đảm bảo performance và chính xác
func mapProfile(row []interface{}, start int, end int) map[string]string {
	res := make(map[string]string)
	
	getVal := func(idx int) string {
		if idx < len(row) { return CleanString(row[idx]) }
		return ""
	}

	// 1. AUTH PROFILE (0-22)
	if start == 0 {
		res["status"] = getVal(INDEX_DATA_TIKTOK.STATUS)
		res["note"] = getVal(INDEX_DATA_TIKTOK.NOTE)
		res["device_id"] = getVal(INDEX_DATA_TIKTOK.DEVICE_ID)
		res["user_id"] = getVal(INDEX_DATA_TIKTOK.USER_ID)
		res["user_sec"] = getVal(INDEX_DATA_TIKTOK.USER_SEC)
		res["user_name"] = getVal(INDEX_DATA_TIKTOK.USER_NAME)
		res["email"] = getVal(INDEX_DATA_TIKTOK.EMAIL)
		res["nick_name"] = getVal(INDEX_DATA_TIKTOK.NICK_NAME)
		res["password"] = getVal(INDEX_DATA_TIKTOK.PASSWORD)
		res["password_email"] = getVal(INDEX_DATA_TIKTOK.PASSWORD_EMAIL)
		res["recovery_email"] = getVal(INDEX_DATA_TIKTOK.RECOVERY_EMAIL)
		res["two_fa"] = getVal(INDEX_DATA_TIKTOK.TWO_FA)
		res["phone"] = getVal(INDEX_DATA_TIKTOK.PHONE)
		res["birthday"] = getVal(INDEX_DATA_TIKTOK.BIRTHDAY)
		res["client_id"] = getVal(INDEX_DATA_TIKTOK.CLIENT_ID)
		res["refresh_token"] = getVal(INDEX_DATA_TIKTOK.REFRESH_TOKEN)
		res["access_token"] = getVal(INDEX_DATA_TIKTOK.ACCESS_TOKEN)
		res["cookie"] = getVal(INDEX_DATA_TIKTOK.COOKIE)
		res["user_agent"] = getVal(INDEX_DATA_TIKTOK.USER_AGENT)
		res["proxy"] = getVal(INDEX_DATA_TIKTOK.PROXY)
		res["proxy_expired"] = getVal(INDEX_DATA_TIKTOK.PROXY_EXPIRED)
		res["create_country"] = getVal(INDEX_DATA_TIKTOK.CREATE_COUNTRY)
		res["create_time"] = getVal(INDEX_DATA_TIKTOK.CREATE_TIME)
	}

	// 2. ACTIVITY PROFILE (23-44)
	if start == 23 {
		res["status_post"] = getVal(INDEX_DATA_TIKTOK.STATUS_POST)
		res["daily_post_limit"] = getVal(INDEX_DATA_TIKTOK.DAILY_POST_LIMIT)
		res["today_post_count"] = getVal(INDEX_DATA_TIKTOK.TODAY_POST_COUNT)
		res["daily_follow_limit"] = getVal(INDEX_DATA_TIKTOK.DAILY_FOLLOW_LIMIT)
		res["today_follow_count"] = getVal(INDEX_DATA_TIKTOK.TODAY_FOLLOW_COUNT)
		res["last_active_date"] = getVal(INDEX_DATA_TIKTOK.LAST_ACTIVE_DATE)
		res["follower_count"] = getVal(INDEX_DATA_TIKTOK.FOLLOWER_COUNT)
		res["following_count"] = getVal(INDEX_DATA_TIKTOK.FOLLOWING_COUNT)
		res["likes_count"] = getVal(INDEX_DATA_TIKTOK.LIKES_COUNT)
		res["video_count"] = getVal(INDEX_DATA_TIKTOK.VIDEO_COUNT)
		res["status_live"] = getVal(INDEX_DATA_TIKTOK.STATUS_LIVE)
		// Các trường mới trong V243
		res["live_phone_access"] = getVal(INDEX_DATA_TIKTOK.LIVE_PHONE_ACCESS)
		res["live_studio_access"] = getVal(INDEX_DATA_TIKTOK.LIVE_STUDIO_ACCESS)
		res["live_key"] = getVal(INDEX_DATA_TIKTOK.LIVE_KEY)
		res["last_live_duration"] = getVal(INDEX_DATA_TIKTOK.LAST_LIVE_DURATION)
		res["shop_role"] = getVal(INDEX_DATA_TIKTOK.SHOP_ROLE)
		res["shop_id"] = getVal(INDEX_DATA_TIKTOK.SHOP_ID)
		res["product_count"] = getVal(INDEX_DATA_TIKTOK.PRODUCT_COUNT)
		res["shop_health"] = getVal(INDEX_DATA_TIKTOK.SHOP_HEALTH)
		res["total_orders"] = getVal(INDEX_DATA_TIKTOK.TOTAL_ORDERS)
		res["total_revenue"] = getVal(INDEX_DATA_TIKTOK.TOTAL_REVENUE)
		res["commission_rate"] = getVal(INDEX_DATA_TIKTOK.COMMISSION_RATE)
	}

	// 3. AI PROFILE (45-60)
	if start == 45 {
		res["signature"] = getVal(INDEX_DATA_TIKTOK.SIGNATURE)
		res["default_category"] = getVal(INDEX_DATA_TIKTOK.DEFAULT_CATEGORY)
		res["default_product"] = getVal(INDEX_DATA_TIKTOK.DEFAULT_PRODUCT)
		res["preferred_keywords"] = getVal(INDEX_DATA_TIKTOK.PREFERRED_KEYWORDS)
		res["preferred_hashtags"] = getVal(INDEX_DATA_TIKTOK.PREFERRED_HASHTAGS)
		res["writing_style"] = getVal(INDEX_DATA_TIKTOK.WRITING_STYLE)
		res["main_goal"] = getVal(INDEX_DATA_TIKTOK.MAIN_GOAL)
		res["default_cta"] = getVal(INDEX_DATA_TIKTOK.DEFAULT_CTA)
		res["content_length"] = getVal(INDEX_DATA_TIKTOK.CONTENT_LENGTH)
		res["content_type"] = getVal(INDEX_DATA_TIKTOK.CONTENT_TYPE)
		res["target_audience"] = getVal(INDEX_DATA_TIKTOK.TARGET_AUDIENCE)
		res["visual_style"] = getVal(INDEX_DATA_TIKTOK.VISUAL_STYLE)
		res["ai_persona"] = getVal(INDEX_DATA_TIKTOK.AI_PERSONA)
		res["banned_keywords"] = getVal(INDEX_DATA_TIKTOK.BANNED_KEYWORDS)
		res["content_language"] = getVal(INDEX_DATA_TIKTOK.CONTENT_LANGUAGE)
		res["country"] = getVal(INDEX_DATA_TIKTOK.COUNTRY)
	}

	return res
}
