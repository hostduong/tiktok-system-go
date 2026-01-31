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
   - Lấy tài khoản để chạy tool (Login, Reg, Auto).
   - Hệ thống tự động phân phối nick theo quy trình ưu tiên (Cũ -> Mới).
   - Tự động gán DeviceID vào nick nếu nick đó đang trống.

2. CẤU TRÚC BODY REQUEST:
{
  "type": "auto",             // Loại lệnh: "login", "register", "auto", "auto_reset", "login_reset"
  "token": "...",             // Token xác thực
  "deviceId": "...",          // ID thiết bị
  
  // --- TÙY CHỌN: LẤY CHÍNH XÁC (Ưu tiên 1) ---
  "row_index": 123,           // Nếu có, hệ thống sẽ cố gắng lấy chính xác dòng này (nếu khớp filter)

  // --- TÙY CHỌN: BỘ LỌC NÂNG CAO (Ưu tiên 2) ---
  "search_and": {             // Nick phải thỏa mãn TẤT CẢ điều kiện
      "match_col_6": ["gmail.com"],   // Cột 6 phải là gmail
      "min_col_29": 1000              // Cột 29 (Follow) >= 1000
  },
  "search_or": { ... },       // Nick thỏa mãn 1 TRONG CÁC điều kiện

  // --- TÙY CHỌN: CẬP NHẬT DỮ LIỆU KHI LẤY ---
  "updated": {
      "col_18": "UserAgent mới" // Cập nhật ngay UserAgent khi lấy nick
  }
}

3. QUY TRÌNH ƯU TIÊN (PRIORITY STEPS):
   - AUTO: Tìm "Đang chạy" -> "Đang chờ" -> "Đăng nhập" (Kho) -> "Đang Reg" -> "Chờ Reg" -> "Reg" (Kho).
   - LOGIN: Tìm "Đang chạy" -> "Đang chờ" -> "Đăng nhập".
   - REGISTER: Tìm "Đang Reg" -> "Chờ Reg" -> "Reg".
*/

// =================================================================================================
// 🟢 CẤU TRÚC PHẢN HỒI (RESPONSE)
// =================================================================================================

type LoginResponse struct {
	Status          string          `json:"status"`          // "true" hoặc "false"
	Type            string          `json:"type"`            // Loại hành động (login/register)
	Messenger       string          `json:"messenger"`       // Thông báo
	DeviceId        string          `json:"deviceId"`        // ID thiết bị nhận nick
	RowIndex        int             `json:"row_index"`       // Dòng dữ liệu trong Excel
	SystemEmail     string          `json:"system_email"`    // Email hệ thống (nếu có)
	AuthProfile     AuthProfile     `json:"auth_profile"`    // Thông tin đăng nhập
	ActivityProfile ActivityProfile `json:"activity_profile"`// Thông tin hoạt động
	AiProfile       AiProfile       `json:"ai_profile"`      // Cấu hình AI
}

// Cấu trúc bước ưu tiên tìm kiếm
type PriorityStep struct {
	Status  string // Trạng thái cần tìm (Ví dụ: "đang chạy")
	IsMy    bool   // True: Chỉ tìm nick của DeviceId này
	IsEmpty bool   // True: Chỉ tìm nick chưa có chủ (Kho chung)
	PrioID  int    // Mức độ ưu tiên (để debug)
}

// =================================================================================================
// 🟢 HANDLER CHÍNH (Tiếp nhận Request)
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON Body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi định dạng JSON"}`, 400)
		return
	}

	// 2. Lấy Context xác thực
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	// 3. Chuẩn hóa dữ liệu đầu vào
	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	// Xác định hành động chuẩn
	action := "login"
	if reqType == "register" { action = "register" } else if reqType == "auto" { action = "auto" } else if reqType == "auto_reset" { action = "auto_reset" } else if reqType == "login_reset" { action = "login_reset" }
	
	// Lấy dữ liệu update kèm theo (nếu có)
	updateMap := parseUpdateDataLogin(body)

	// 4. Gọi hàm xử lý logic cốt lõi
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action, updateMap)

	// 5. Trả về kết quả
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"status":    "false",
			"messenger": err.Error(), // Trả về lý do lỗi (VD: "Các tài khoản đã hoàn thành")
		})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC LÕI (Tìm kiếm và Phân phối nick)
// =================================================================================================

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string, updateMap map[int]interface{}) (*LoginResponse, error) {
	// BƯỚC 1: Tải dữ liệu từ Cache
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu hệ thống") }

	// BƯỚC 2: Chuẩn bị bộ lọc
	filters := parseFilterParams(body)
	STATE.SheetMutex.RLock() // Khóa đọc để an toàn dữ liệu
	rawLen := len(cacheData.RawValues)

	// --- CHIẾN LƯỢC 1: TÌM THEO ROW_INDEX (Ưu tiên tuyệt đối) ---
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			idx := int(val) - RANGES.DATA_START_ROW
			if idx >= 0 && idx < rawLen {
				// Kiểm tra Filter (nếu có)
				if filters.HasFilter {
					if !isRowMatched(cacheData.CleanValues[idx], cacheData.RawValues[idx], filters) {
						STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Dòng yêu cầu không khớp điều kiện lọc")
					}
				}
				// Kiểm tra chất lượng nick (Có user/pass/email không?)
				valQ := KiemTraChatLuongClean(cacheData.CleanValues[idx], action)
				
				// Chốt đơn
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), valQ.SystemEmail, action, 0, updateMap)
			}
			STATE.SheetMutex.RUnlock(); return nil, fmt.Errorf("Dòng yêu cầu không tồn tại")
		}
	}

	// --- CHIẾN LƯỢC 2: TÌM THEO QUY TRÌNH ƯU TIÊN (Phễu Lọc) ---
	steps := buildPrioritySteps(action)

	for _, step := range steps {
		// Lấy danh sách các dòng có Status khớp bước này
		indices := cacheData.StatusMap[step.Status]

		for _, idx := range indices {
			if idx < rawLen {
				row := cacheData.CleanValues[idx]
				
				isMyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId)
				isEmptyDevice := (row[INDEX_DATA_TIKTOK.DEVICE_ID] == "")
				
				// Kiểm tra quyền sở hữu (Của mình hoặc Của kho)
				if (step.IsMy && isMyDevice) || (step.IsEmpty && isEmptyDevice) {
					
					// Kiểm tra Bộ lọc nội dung (Search And/Or)
					if filters.HasFilter {
						if !isRowMatched(row, cacheData.RawValues[idx], filters) { continue }
					}
					
					// Kiểm tra Chất lượng Nick
					val := KiemTraChatLuongClean(row, action)
					if !val.Valid {
						// Nếu nick lỗi -> Tự động đánh dấu "Chú ý" (Self Healing)
						STATE.SheetMutex.RUnlock(); doSelfHealing(sid, idx, val.Missing, cacheData); STATE.SheetMutex.RLock()
						continue
					}

					// --> TÌM THẤY NICK PHÙ HỢP! --> THỰC HIỆN GÁN
					STATE.SheetMutex.RUnlock(); STATE.SheetMutex.Lock()
					
					// Kiểm tra lại lần cuối trong Lock Write (Double Check Locking)
					currRow := cacheData.CleanValues[idx]
					if (step.IsMy && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId) || (step.IsEmpty && currRow[INDEX_DATA_TIKTOK.DEVICE_ID] == "") {
						// Gán chủ quyền ngay lập tức
						cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
						cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
						cacheData.AssignedMap[deviceId] = idx // Cập nhật Map nhanh
						
						STATE.SheetMutex.Unlock()
						// Gọi hàm chốt giao dịch và trả về
						return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID, updateMap)
					}
					STATE.SheetMutex.Unlock(); STATE.SheetMutex.RLock()
				}
			}
		}
	}
	
	// --- CHIẾN LƯỢC 3: KIỂM TRA ĐÃ HOÀN THÀNH CHƯA? ---
	// Nếu chạy hết phễu mà không tìm thấy nick nào, kiểm tra xem có nick "Hoàn thành" không
	checkList := []string{"login", "auto", "login_reset", "register"}
	isCheck := false
	for _, s := range checkList { if strings.Contains(action, s) { isCheck = true; break } }
	
	if isCheck {
		completedIndices := cacheData.StatusMap[STATUS_READ.COMPLETED] // Status: "hoàn thành"
		for _, idx := range completedIndices {
			if idx < rawLen && cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
				STATE.SheetMutex.RUnlock()
				return nil, fmt.Errorf("Các tài khoản đã hoàn thành")
			}
		}
	}

	STATE.SheetMutex.RUnlock()
	return nil, fmt.Errorf("Không còn tài khoản phù hợp")
}

// =================================================================================================
// 🛠 CÁC HÀM HỖ TRỢ (HELPER FUNCTIONS)
// =================================================================================================

// Xây dựng danh sách các bước tìm kiếm theo thứ tự ưu tiên
func buildPrioritySteps(action string) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	// Hàm helper thêm bước
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	// Logic Login: Ưu tiên nick đang chạy dở -> Đang chờ -> Mới tinh
	if action == "login" || action == "login_reset" {
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3) // Nick của mình
		add(STATUS_READ.LOGIN, false, true, 4) // Nick kho chung
		if action == "login_reset" { add(STATUS_READ.COMPLETED, true, false, 5) }
	} else 
	// Logic Register
	if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1)
		add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3)
		add(STATUS_READ.REGISTER, false, true, 4)
	} else 
	// Logic Auto (Kết hợp Login trước, Reg sau)
	if action == "auto" || action == "auto_reset" {
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		if action == "auto_reset" { add(STATUS_READ.COMPLETED, true, false, 99) }
		
		// Hết nick login thì tìm nick reg
		add(STATUS_READ.REGISTERING, true, false, 5)
		add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7)
		add(STATUS_READ.REGISTER, false, true, 8)
	}
	return steps
}

// Hàm chốt giao dịch: Cập nhật trạng thái, Ghi Note, Lưu xuống Queue
func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string, priority int, updateMap map[int]interface{}) (*LoginResponse, error) {
	row := cache.RawValues[idx]
	
	// Xác định trạng thái mới
	tSt := STATUS_WRITE.RUNNING
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }

	// Tạo Note mới (Logic đếm số lần)
	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false
	if (strings.Contains(action, "reset")) && (priority == 5 || priority == 99) {
		mode = "reset"; isResetCompleted = true
	}
	tNote := tao_ghi_chu_chuan_login(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	defer STATE.SheetMutex.Unlock()

	// 1. Dọn dẹp các nick cũ đang treo của Device này (Chuyển về Waiting)
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		cNote := tao_ghi_chu_chuan_login(cOldNote, cSt, "normal")
		if isResetCompleted { cNote = tao_ghi_chu_chuan_login(cOldNote, "Reset chờ chạy", "reset") }
		
		// Update Cache cho nick cũ
		updateRowCache(cache, cIdx, cSt, cNote, "")
		
		// Ghi xuống Queue
		cRow := make([]interface{}, len(cache.RawValues[cIdx])); copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// 2. Cập nhật nick mới lấy (Target Row)
	// Update các cột tùy chọn (nếu có trong request)
	for colIdx, val := range updateMap {
		if colIdx >= 0 && colIdx < len(cache.RawValues[idx]) {
			// Không cho phép update các cột hệ thống ở đây
			if colIdx == INDEX_DATA_TIKTOK.STATUS || colIdx == INDEX_DATA_TIKTOK.NOTE || colIdx == INDEX_DATA_TIKTOK.DEVICE_ID { continue }
			cache.RawValues[idx][colIdx] = val
			if colIdx < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][colIdx] = CleanString(val) }
		}
	}
	
	// Update Status, Note, DeviceID
	updateRowCache(cache, idx, tSt, tNote, deviceId)

	// Tạo bản sao để trả về Response và Ghi Queue
	newRow := make([]interface{}, len(cache.RawValues[idx])); copy(newRow, cache.RawValues[idx])
	
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	msg := "Lấy nick thành công"
	return &LoginResponse{
		Status: "true", Type: typ, Messenger: msg, DeviceId: deviceId, RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: MakeAuthProfile(newRow), ActivityProfile: MakeActivityProfile(newRow), AiProfile: MakeAiProfile(newRow),
	}, nil
}

// Helper update cache nội bộ
func updateRowCache(cache *SheetCacheData, idx int, newSt, newNote, newDev string) {
	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = newSt
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = newNote
	if newDev != "" { cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = newDev }

	// Update CleanValues & StatusMap
	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(newSt) }
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(newNote) }
	
	if oldCleanSt != CleanString(newSt) {
		removeFromStatusMap(cache.StatusMap, oldCleanSt, idx)
		newStClean := CleanString(newSt)
		cache.StatusMap[newStClean] = append(cache.StatusMap[newStClean], idx)
	}
}

// Phân tích dữ liệu update bổ sung từ request
func parseUpdateDataLogin(body map[string]interface{}) map[int]interface{} {
	cols := make(map[int]interface{})
	if v, ok := body["updated"]; ok {
		if updatedMap, ok := v.(map[string]interface{}); ok {
			for k, val := range updatedMap {
				if strings.HasPrefix(k, "col_") {
					if idxStr := strings.TrimPrefix(k, "col_"); idxStr != "" {
						if idx, err := strconv.Atoi(idxStr); err == nil {
							cols[idx] = val
						}
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
		cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = STATUS_WRITE.ATTENTION
		cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = msg
		updateRowCache(cache, idx, STATUS_WRITE.ATTENTION, msg, "")
	}
	fullRow := make([]interface{}, len(cache.RawValues[idx])); copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()
	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

// 🔥 HÀM TẠO NOTE CHUẨN (ĐỒNG BỘ REGEX VỚI UPDATE)
func tao_ghi_chu_chuan_login(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" { return fmt.Sprintf("%s\n%s", newStatus, nowFull) }
	
	oldNote = SafeString(oldNote)
	count := 0
	
	// 1. Dùng Regex lấy số lần chạy cũ (Chính xác 100%)
	match := REGEX_COUNT.FindStringSubmatch(oldNote)
	if len(match) > 1 {
		if c, err := strconv.Atoi(match[1]); err == nil {
			count = c
		}
	}
	if count == 0 { count = 1 }

	// 2. Logic kiểm tra ngày để Reset
	today := nowFull[:10]
	oldDate := ""
	// Vẫn quét dòng để tìm ngày tháng cũ
	lines := strings.Split(oldNote, "\n")
	for _, l := range lines { 
		matchDate := REGEX_DATE.FindString(l) // Dùng Regex Date trong config
		if matchDate != "" { oldDate = matchDate; break }
	}

	if oldDate != today { 
		count = 1 // Qua ngày mới -> Reset về 1
	} else { 
		if mode == "reset" { 
			count++ // Lệnh Reset -> Tăng số lần
		} 
		// Nếu là login thường -> Giữ nguyên count
	}

	st := newStatus
	if st == "" && len(lines) > 0 { st = lines[0] }
	if st == "" { st = "Đang chạy" }
	
	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
