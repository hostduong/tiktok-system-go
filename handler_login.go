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

// Map các trạng thái ưu tiên cho từng hành động
var PRIORITY_GROUPS = map[string][]string{
	"login":    {STATUS_READ.RUNNING, STATUS_READ.WAITING, STATUS_READ.LOGIN},
	"register": {STATUS_READ.REGISTERING, STATUS_READ.WAIT_REG, STATUS_READ.REGISTER},
	"auto":     {STATUS_READ.RUNNING, STATUS_READ.WAITING, STATUS_READ.LOGIN, STATUS_READ.REGISTERING, STATUS_READ.WAIT_REG, STATUS_READ.REGISTER},
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
	// Nạp dữ liệu (Smart Load đã phân vùng sẵn)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}

	var targetIndex = -1
	var responseType = "login"
	var sysEmail = ""
	
	// --- 1. [OPTIMIZED] TÌM NICK CŨ CỦA THIẾT BỊ (ĐỘ ƯU TIÊN CAO NHẤT) ---
	// Thay vì quét vòng lặp, ta tra cứu trực tiếp trong AssignedMap (O(1))
	// Đây là cải tiến lớn nhất về tốc độ.
	if idx, ok := cacheData.AssignedMap[deviceId]; ok {
		// Kiểm tra tính hợp lệ (Row còn tồn tại và DeviceID chưa bị xóa)
		if idx < len(cacheData.RawValues) {
			currentDev := CleanString(cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID])
			if currentDev == deviceId {
				// Tìm thấy nick cũ -> Kiểm tra xem có thỏa mãn điều kiện action không
				cleanRow := cacheData.CleanValues[idx]
				val := kiem_tra_chat_luong_clean(cleanRow, action)
				
				// Nếu nick vẫn ngon -> Lấy luôn
				if val.Valid {
					targetIndex = idx
					sysEmail = val.SystemEmail
					st := cleanRow[INDEX_DATA_TIKTOK.STATUS]
					if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG {
						responseType = "register"
					}
					// Fast Path Return
					return commit_and_response(sid, deviceId, cacheData, targetIndex, responseType, sysEmail, action)
				}
				// Nếu nick cũ bị lỗi -> Vẫn giữ index để báo lỗi hoặc ignore, 
				// nhưng theo logic Node.js: nick cũ lỗi thì coi như mất, tìm nick mới.
				// Ở đây ta reset targetIndex để xuống bước dưới tìm nick mới.
				targetIndex = -1
			}
		}
	}

	// --- 2. TÌM KIẾM NÂNG CAO (Search Col) ---
	// Nếu client gửi yêu cầu tìm kiếm cụ thể (search_col_X)
	searchCols := make(map[int]string)
	for k, v := range body {
		if strings.HasPrefix(k, "search_col_") {
			if i, err := strconv.Atoi(strings.TrimPrefix(k, "search_col_")); err == nil {
				searchCols[i] = CleanString(v)
			}
		} else if k == "search_user_id" { // Hỗ trợ legacy
			searchCols[INDEX_DATA_TIKTOK.USER_ID] = CleanString(v)
		} else if k == "search_email" { // Hỗ trợ legacy
			searchCols[INDEX_DATA_TIKTOK.EMAIL] = CleanString(v)
		}
	}

	if len(searchCols) > 0 {
		// Duyệt mảng để tìm (vì search là tác vụ ít khi dùng, O(N) chấp nhận được)
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
				// Tìm thấy -> Kiểm tra chất lượng
				val := kiem_tra_chat_luong_clean(row, action)
				if val.Valid {
					targetIndex = i
					sysEmail = val.SystemEmail
					// Xác định type
					st := row[INDEX_DATA_TIKTOK.STATUS]
					if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING {
						responseType = "register"
					}
					// Search xong -> Commit luôn (chiếm quyền nếu nick đó đang trống)
					return commit_and_response(sid, deviceId, cacheData, targetIndex, responseType, sysEmail, action)
				}
			}
		}
		// Search thất bại
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo yêu cầu")
	}

	// --- 3. [OPTIMIZED] LẤY NICK MỚI (AUTO PICK) ---
	// Thay vì quét toàn bộ bảng, ta chỉ quét các nhóm Status ưu tiên
	if targetIndex == -1 && action != "view_only" {
		isReset := false
		if v, ok := body["is_reset"].(bool); ok && v {
			isReset = true
		}

		// Lấy danh sách status cần kiểm tra theo thứ tự ưu tiên
		priorities := getPriorityList(action, isReset)

		for _, statusKey := range priorities {
			// Lấy danh sách các dòng có status này từ Map (O(1))
			candidateIndices := cacheData.StatusMap[statusKey]
			
			for _, idx := range candidateIndices {
				// Kiểm tra DeviceID (Chỉ lấy nick trống)
				// Truy cập thẳng vào RAM (O(1))
				currentDev := ""
				if idx < len(cacheData.RawValues) {
					// Check trong CleanValues cho nhanh (đã load sẵn)
					// Cột DeviceID là cột 2
					if INDEX_DATA_TIKTOK.DEVICE_ID < len(cacheData.CleanValues[idx]) {
						currentDev = cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
					}
				}

				if currentDev == "" {
					// Nick trống -> Kiểm tra chất lượng
					val := kiem_tra_chat_luong_clean(cacheData.CleanValues[idx], action)
					if val.Valid {
						// 🔥 OPTIMISTIC LOCKING: Chiếm quyền ngay
						STATE.SheetMutex.Lock()
						// Double Check (Trong lock) để chắc chắn chưa ai lấy
						doubleCheckDev := ""
						if idx < len(cacheData.CleanValues) && INDEX_DATA_TIKTOK.DEVICE_ID < len(cacheData.CleanValues[idx]) {
							doubleCheckDev = cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
						}
						
						if doubleCheckDev == "" {
							// Ghi tên mình vào RAM ngay lập tức
							cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							
							// Cập nhật AssignedMap để lần sau tìm cho nhanh
							cacheData.AssignedMap[deviceId] = idx
							
							// (Tùy chọn) Xóa khỏi UnassignedList nếu muốn quản lý chặt hơn
							// Nhưng vì ta dựa vào DeviceID == "" nên không cần thiết phải thao tác list đó lúc này.
							
							targetIndex = idx
							sysEmail = val.SystemEmail
							if statusKey == STATUS_READ.REGISTERING || statusKey == STATUS_READ.WAIT_REG || statusKey == STATUS_READ.REGISTER {
								responseType = "register"
							}
							
							STATE.SheetMutex.Unlock()
							goto FOUND // Thoát 2 vòng lặp
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

	// --- 4. TRẢ VỀ KẾT QUẢ ---
	return commit_and_response(sid, deviceId, cacheData, targetIndex, responseType, sysEmail, action)
}

// 🟢 HELPER: Commit Data & Build Response
func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string) (*LoginResponse, error) {
	row := cache.RawValues[idx]
	
	// Xác định trạng thái ghi
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" {
		tSt = STATUS_WRITE.REGISTERING
	}

	// Xác định Note
	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	// Nếu là Auto Reset (ưu tiên thấp nhất) -> mode reset
	// Logic đơn giản: Nếu trạng thái cũ là Completed -> Reset
	cleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	if cleanSt == STATUS_READ.COMPLETED {
		mode = "reset"
	}
	
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	// Update RAM (Values & Status Map)
	STATE.SheetMutex.Lock()
	
	// 1. Update Values
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = tSt
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = tNote
	cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId // Đảm bảo chắc chắn
	
	// 2. Update Clean Values
	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt)
	}
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(tNote)
	}
	
	// 3. Update Status Map (Chuyển nhà cho index)
	// Xóa khỏi bucket status cũ
	if cleanSt != "" {
		removeFromStatusMap(cache.StatusMap, cleanSt, idx)
	}
	// Thêm vào bucket status mới
	newCleanSt := CleanString(tSt)
	cache.StatusMap[newCleanSt] = append(cache.StatusMap[newCleanSt], idx)
	
	STATE.SheetMutex.Unlock()

	// Update Queue (Ghi xuống đĩa)
	// Clone row để tránh race condition khi queue đọc
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

// Helper: Xóa index khỏi slice trong map (cần tối ưu nếu list quá dài, nhưng tạm thời ok)
func removeFromStatusMap(m map[string][]int, status string, targetIdx int) {
	if list, ok := m[status]; ok {
		for i, v := range list {
			if v == targetIdx {
				// Xóa phần tử i
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
		// Login trước, Register sau
		list = append(list, STATUS_READ.RUNNING, STATUS_READ.WAITING, STATUS_READ.LOGIN)
		list = append(list, STATUS_READ.REGISTERING, STATUS_READ.WAIT_REG, STATUS_READ.REGISTER)
	}

	if isReset {
		list = append(list, STATUS_READ.COMPLETED)
	}
	return list
}

// Các hàm kiem_tra_chat_luong_clean, tao_ghi_chu_chuan giữ nguyên từ phiên bản trước
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
