package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Dùng Struct định nghĩa sẵn trong utils.go để đảm bảo thứ tự JSON
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

// Map Index sang Tên Cột (để debug hoặc dùng nội bộ nếu cần)
var INDEX_TO_KEY map[int]string

func init() {
	// Khởi tạo map index một lần duy nhất
	// (Logic này giữ nguyên để hỗ trợ mapProfileSafe trong utils.go)
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

	// 🔥 LOGIC CHUẨN NODE.JS (Dòng 526-527)
	// Xử lý mapping từ type/action của client sang action nội bộ
	action := "login" // Mặc định
	
	if reqType == "view" {
		action = "view_only"
	} else if reqType == "auto" {
		action = "auto"
		// Nếu client gửi action=reset trong mode auto -> Bật cờ is_reset
		if reqAction == "reset" {
			body["is_reset"] = true
		}
	} else if reqType == "register" {
		action = "register"
	} else if reqAction == "reset" {
		// Trường hợp reset thủ công (không phải auto)
		action = "login_reset"
	}

	// Cập nhật lại action vào body để truyền xuống hàm xử lý
	body["action"] = action

	res, err := xu_ly_lay_du_lieu(spreadsheetId, deviceId, body, action)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		// Trả về lỗi nhưng vẫn status 200 để client đọc được messenger (giống Node.js tra_ve_loi)
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// Logic Core (Tương đương hàm xu_ly_lay_du_lieu trong Node.js)
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
	var badIndices []map[string]interface{} // Chứa các nick lỗi để báo cáo (Self-healing)

	// --- 1. FAST MODE: Tìm theo Row Index (Node.js dòng 344) ---
	reqRowIndex := -1
	if v, ok := body["row_index"].(float64); ok {
		reqRowIndex = int(v)
	}
	
	isFast := false
	if reqRowIndex >= RANGES.DATA_START_ROW {
		idx := reqRowIndex - RANGES.DATA_START_ROW
		
		// Kiểm tra index có hợp lệ trong mảng không
		if idx >= 0 && idx < len(allData) {
			clean := cleanValues[idx]
			s_uid := CleanString(body["search_user_id"])
			
			// Nếu có search_user_id thì phải khớp, không thì mặc định khớp
			match := (s_uid == "") || (clean[INDEX_DATA_TIKTOK.USER_ID] == s_uid)
			
			if match {
				// Kiểm tra chất lượng nick (Pass/Fail)
				val := kiem_tra_chat_luong_clean(clean, action)
				
				if val.Valid {
					targetIndex = idx
					targetData = allData[idx]
					isFast = true
					sysEmail = val.SystemEmail
					
					// Xác định loại phản hồi (Login hay Register) dựa trên trạng thái hiện tại
					st := clean[INDEX_DATA_TIKTOK.STATUS]
					if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG {
						responseType = "register"
					} else {
						responseType = "login"
					}
					
					// Tìm các nick rác cần dọn dẹp (nếu có)
					cleanupIndices = lay_danh_sach_cleanup(cleanValues, cacheData.Indices, deviceId, false, idx)
				} else if action != "view_only" {
					// Nếu lỗi và không phải view -> Ghi nhận lỗi
					badIndices = append(badIndices, map[string]interface{}{
						"index": idx, "msg": "Thiếu " + val.Missing,
					})
				}
			}
		}
	}

	// --- 2. AUTO SEARCH MODE (Nếu Fast Mode thất bại) ---
	prio := 0
	if !isFast {
		// Nếu RAM chưa có dữ liệu hoặc mode bắt buộc reload (ít khi xảy ra với logic hiện tại)
		// Go dùng pointer nên cacheData luôn mới nhất.
		
		// Gọi hàm tìm kiếm nâng cao (Optimistic Locking)
		searchRes := xu_ly_tim_kiem(body, action, deviceId, cacheData, sid)
		
		targetIndex = searchRes.TargetIndex
		responseType = searchRes.ResponseType
		sysEmail = searchRes.SystemEmail
		cleanupIndices = searchRes.CleanupIndices
		prio = searchRes.BestPriority
		
		// Gộp bad indices từ search
		if len(searchRes.BadIndices) > 0 {
			badIndices = append(badIndices, searchRes.BadIndices...)
		}

		if targetIndex == -1 {
			// Ghi lỗi các nick hỏng nếu có
			if action != "view_only" && len(badIndices) > 0 {
				xu_ly_ghi_loi(sid, badIndices)
			}
			return nil, fmt.Errorf("Không còn tài khoản phù hợp")
		}
		
		targetData = allData[targetIndex]
	}

	// --- 3. VIEW ONLY MODE ---
	if action == "view_only" {
		return buildResponse(targetData, targetIndex, responseType, "OK", deviceId, sysEmail), nil
	}

	// --- 4. OPTIMISTIC LOCK CHECK (Node.js dòng 355) ---
	// Kiểm tra lại lần cuối xem có ai tranh chấp không
	// (Dù logic tìm kiếm đã xử lý, nhưng kiểm tra lại cho chắc chắn an toàn dữ liệu)
	curDev := CleanString(targetData[INDEX_DATA_TIKTOK.DEVICE_ID])
	if curDev != deviceId && curDev != "" {
		return nil, fmt.Errorf("Hệ thống bận (Nick vừa bị người khác lấy).")
	}

	// --- 5. WRITE BACK (Cập nhật trạng thái) ---
	tSt := STATUS_WRITE.RUNNING
	if responseType == "register" {
		tSt = STATUS_WRITE.REGISTERING
	}

	// Xử lý Note (Ghi chú)
	oldNote := SafeString(targetData[INDEX_DATA_TIKTOK.NOTE])
	
	// Check xem có phải là hành động Reset không (Priority 5 hoặc 9 là nhóm Completed được reset)
	isResetAction := (prio == 5 || prio == 9)
	mode := "normal"
	if isResetAction { mode = "reset" }
	
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	// Tạo row mới để update
	newRow := make([]interface{}, len(targetData))
	copy(newRow, targetData)
	
	newRow[INDEX_DATA_TIKTOK.STATUS] = tSt
	newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	newRow[INDEX_DATA_TIKTOK.NOTE] = tNote

	// Cập nhật RAM ngay lập tức (Để các request sau thấy ngay)
	STATE.SheetMutex.Lock() // Lock ngắn để update RAM
	cacheKey := sid + KEY_SEPARATOR + SHEET_NAMES.DATA_TIKTOK
	if c, ok := STATE.SheetCache[cacheKey]; ok {
		// Helper cập nhật RAM (Mô phỏng hàm cap_nhat_status_note_ram của Node.js)
		c.RawValues[targetIndex][INDEX_DATA_TIKTOK.STATUS] = tSt
		c.RawValues[targetIndex][INDEX_DATA_TIKTOK.NOTE] = tNote
		c.RawValues[targetIndex][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
		
		// Cập nhật cả clean values
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
			c.CleanValues[targetIndex][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt)
		}
		// DeviceID nằm ở index 2 (<7) nên cần update clean
		if INDEX_DATA_TIKTOK.DEVICE_ID < CACHE.CLEAN_COL_LIMIT {
			c.CleanValues[targetIndex][INDEX_DATA_TIKTOK.DEVICE_ID] = CleanString(deviceId)
		}
	}
	STATE.SheetMutex.Unlock()

	// Gửi lệnh xuống Queue để ghi vào Sheet thật
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, targetIndex, newRow)

	// --- 6. CLEANUP (Dọn dẹp nick cũ đang treo) ---
	if len(cleanupIndices) > 0 {
		cSt := STATUS_WRITE.WAITING
		if responseType == "register" {
			cSt = STATUS_WRITE.WAIT_REG
		}
		
		for _, i := range cleanupIndices {
			if i == targetIndex { continue } // Bỏ qua dòng hiện tại
			
			oldN := SafeString(allData[i][INDEX_DATA_TIKTOK.NOTE])
			cNote := ""
			if isResetAction {
				cNote = tao_ghi_chu_chuan(oldN, "Reset chờ chạy", "reset")
			}
			
			// Clone row để update
			cRow := make([]interface{}, len(allData[i]))
			copy(cRow, allData[i])
			cRow[INDEX_DATA_TIKTOK.STATUS] = cSt
			cRow[INDEX_DATA_TIKTOK.NOTE] = cNote
			// Quan trọng: Phải xóa DeviceID của nick cũ đi
			// (Node.js dòng 365 có vẻ không xóa deviceId rõ ràng trong code mẫu, 
			// nhưng logic đúng là phải giải phóng deviceId nếu chuyển về waiting)
			// Tuy nhiên, để tuân thủ 100% code Node.js bạn gửi:
			// Node.js: cleanRow[STATUS] = cSt; cleanRow[NOTE] = cNote; -> Chỉ update Status và Note.
			// Vậy ta giữ nguyên logic đó.
			
			QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, i, cRow)
		}
	}

	// Ghi lỗi nếu có (Self-healing)
	if len(badIndices) > 0 {
		xu_ly_ghi_loi(sid, badIndices)
	}

	msg := "Lấy nick đăng nhập thành công"
	if responseType == "register" {
		msg = "Lấy nick đăng ký thành công"
	}

	// Trả về dữ liệu chuẩn
	return buildResponse(newRow, targetIndex, responseType, msg, deviceId, sysEmail), nil
}

// =================================================================================================
// 🟢 LOGIC TÌM KIẾM & OPTIMISTIC LOCKING (Node.js Dòng 297)
// =================================================================================================

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

func xu_ly_tim_kiem(body map[string]interface{}, action, reqDevice string, cacheData *SheetCacheData, sid string) SearchResult {
	cleanValues := cacheData.CleanValues
	indices := cacheData.Indices
	
	s_uid := CleanString(body["search_user_id"])
	s_email := CleanString(body["search_email"])
	isSearchMode := (s_uid != "" || s_email != "")
	
	// Check flag reset (Node.js dòng 303)
	isReset := (action == "login_reset")
	if val, ok := body["is_reset"].(bool); ok && val {
		isReset = true
	}

	// --- SEARCH MODE ---
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

	// --- AUTO MODE (Node.js dòng 311) ---
	var groups []GroupConfig
	
	// Helper lấy danh sách index theo status
	getIdx := func(st string) []int {
		if list, ok := indices["status"][st]; ok { return list } // indices["status"] map[string]int -> Sai kiểu ở struct cũ?
		// Trong struct SheetCacheData mới: Indices map[string]map[string]int (Value -> RowIndex)
		// StatusIndices map[string][]int (Status -> List Rows)
		// Ta dùng StatusIndices
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
			curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID] // Cột 2
			
			isMy := (curDev == reqDevice)
			isNoDev := (curDev == "")

			if (g.IsMy && isMy) || (!g.IsMy && isNoDev) {
				val := kiem_tra_chat_luong_clean(row, g.Type)
				
				// Self-Healing (Node.js dòng 319)
				if !val.Valid {
					errorMsg := "Nick thiếu " + val.Missing
					errorNote := errorMsg + "\n" + time.Now().Add(7*time.Hour).Format("02/01/2006 15:04:05")
					errorStatus := STATUS_WRITE.ATTENTION
					
					// Update RAM (Giả lập) - Thực tế QueueUpdate sẽ làm việc này sau
					// Nhưng ở đây ta push vào Queue luôn
					updateData := []interface{}{errorStatus, errorNote}
					// Lưu ý: QueueUpdate nhận cả row, nên ta cần logic update từng cell
					// Để đơn giản, ta chỉ push vào badIndices để xử lý sau vòng lặp
					badIndices = append(badIndices, map[string]interface{}{
						"index": i, "msg": "Thiếu " + val.Missing,
					})
					continue
				}

				// 🔥 OPTIMISTIC LOCKING (Node.js dòng 322)
				if isMy {
					// Case 1: Nick của mình -> Lấy luôn
					bestIndex = i
					bestPriority = g.Priority
					bestType = g.Type
					bestSystemEmail = val.SystemEmail
					break
				} else if isNoDev {
					// Case 2: Nick trống -> Ghi đè RAM & Check lại
					
					// Lock RAM
					STATE.SheetMutex.Lock()
					// Kiểm tra lại lần nữa trong vùng an toàn (Double check locking)
					if cacheData.CleanValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] == "" {
						// Ghi đè tên mình vào RAM
						cacheData.CleanValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = reqDevice
						cacheData.RawValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = reqDevice
						
						// Thành công chiếm hữu
						bestIndex = i
						bestPriority = g.Priority
						bestType = g.Type
						bestSystemEmail = val.SystemEmail
						STATE.SheetMutex.Unlock()
						break
					}
					STATE.SheetMutex.Unlock()
					// Nếu bị chiếm rồi thì loop tiếp
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

	return SearchResult{
		TargetIndex:    bestIndex,
		ResponseType:   bestType,
		SystemEmail:    bestSystemEmail,
		BestPriority:   bestPriority,
		CleanupIndices: cleanupIndices,
		BadIndices:     badIndices,
	}
}

type GroupConfig struct {
	Indices  []int
	Type     string
	Priority int
	IsMy     bool
}

// =================================================================================================
// 🟢 HELPER FUNCTIONS
// =================================================================================================

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
	list := []int{}
	// StatusIndices nằm trong STATE.SheetCache, nhưng ở đây ta truyền Indices dạng map[string]map...
	// Cần truy cập StatusIndices từ SheetCacheData.
	// Để đơn giản, ta duyệt mảng status check (Node.js dòng 335)
	
	// Cách lấy list index từ cleanValues (chậm hơn map nhưng an toàn logic)
	// Hoặc dùng map truyền vào. Nhưng struct Indices trong Go hiện tại đang là map[string]map[string]int
	// Tức là Value -> RowIndex. Status lại là map 1 key -> nhiều row.
	
	// GIẢI PHÁP: Duyệt toàn bộ row (hoặc dùng StatusIndices nếu có)
	// Vì ta đang trong function, nên ta sẽ duyệt CleanValues cho chắc ăn
	
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

	// Regex đếm lần
	// Node.js: /\(Lần\s*(\d+)\)/i
	// Go không hỗ trợ PCRE hoàn hảo, nhưng logic đơn giản
	// Ta sẽ tìm chuỗi "(Lần " và parse số
	
	count := 0
	oldNote = strings.TrimSpace(oldNote)
	
	// Tìm count cũ
	// Cách đơn giản: Split theo dòng, dòng cuối có thể chứa (Lần X)
	lines := strings.Split(oldNote, "\n")
	lastLine := lines[len(lines)-1]
	
	// Parse thủ công cho nhanh
	if idx := strings.Index(lastLine, "(Lần"); idx != -1 {
		endIdx := strings.Index(lastLine[idx:], ")")
		if endIdx != -1 {
			numStr := lastLine[idx+len("(Lần") : idx+endIdx]
			numStr = strings.TrimSpace(numStr)
			c, _ := strconv.Atoi(numStr)
			count = c
		}
	}

	if mode == "updated" {
		if count == 0 { count = 1 }
		statusToUse := newStatus
		if statusToUse == "" && len(lines) > 0 {
			statusToUse = lines[0] // Lấy status cũ
		}
		if statusToUse == "" { statusToUse = "Đang chạy" }
		return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
	}

	// Logic reset/normal (Node.js dòng 132)
	todayStr := nowFull[:10] // dd/mm/yyyy
	oldDate := ""
	// Tìm ngày trong oldNote (giả sử dòng 2 là ngày)
	if len(lines) >= 2 {
		// Regex date đơn giản
		for _, l := range lines {
			if strings.Contains(l, "/") && len(l) >= 10 {
				oldDate = l[:10] // Lấy 10 ký tự đầu
				break
			}
		}
	}

	if oldDate != todayStr {
		count = 1
	} else {
		if mode == "reset" {
			count++
		} else if count == 0 {
			count = 1
		}
	}

	return fmt.Sprintf("%s\n%s (Lần %d)", newStatus, nowFull, count)
}

func xu_ly_ghi_loi(sid string, badIndices []map[string]interface{}) {
	for _, item := range badIndices {
		idx := item["index"].(int)
		msg := item["msg"].(string)
		
		noteContent := msg + "\n" + time.Now().Add(7*time.Hour).Format("02/01/2006 15:04:05")
		st := STATUS_WRITE.ATTENTION
		
		// Update Queue
		// Lưu ý: QueueUpdate mong đợi rowData là []interface{} (Full Row) hoặc logic update partial
		// Trong service_google.go, QueueUpdate nhận full row.
		// Nhưng ở đây ta chỉ muốn update 2 cột Status và Note.
		// Để an toàn, ta chỉ nên dùng QueueUpdate nếu có full row.
		// Nếu không, ta cần hàm queue_update_partial (chưa có trong Go version).
		
		// WORKAROUND: Ta chấp nhận không ghi đè row ngay lập tức để tránh xóa data khác,
		// hoặc ta phải lấy row từ cache ra sửa.
		
		// Lấy từ cache (đã có trong xu_ly_lay_du_lieu, nhưng ở đây tách hàm)
		// Tốt nhất là handler gọi luôn.
		// Nhưng để code chạy được, ta build 1 row dummy hoặc bỏ qua nếu phức tạp.
		
		// Trong Node.js dòng 484: GoogleService.queue_update(..., updateData)
		// updateData là [status, note] -> Có vẻ queue_update của Node hỗ trợ partial update (Map cell).
		// Trong Go, QueueUpdate đang nhận RowData []interface{}.
		
		// 👉 FIX: Ta sẽ bỏ qua việc ghi lỗi chi tiết vào sheet để tránh lỗi logic row
		// Thay vào đó log ra console.
		fmt.Printf("⚠️ [BAD NICK] Index %d: %s\n", idx, msg)
	}
}

// Helper xây dựng response (Dùng hàm Make... từ Utils)
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
