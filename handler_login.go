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
📘 TÀI LIỆU HƯỚNG DẪN REQUEST BODY (API DOCUMENTATION)
=================================================================================================
Endpoint: POST /tool/login

1. CẤU TRÚC CƠ BẢN:
{
    "type": "login" | "register" | "auto" | "view",  // Loại hành động
    "action": "reset",                               // (Tùy chọn) Nếu có, sẽ tìm cả nick đã Hoàn thành để chạy lại
    "deviceId": "device_123",                        // ID thiết bị (Bắt buộc)
    "row_index": 100,                                // (Tùy chọn) Lấy chính xác dòng số 100
}

2. CẤU TRÚC BỘ LỌC NÂNG CAO (ADVANCED FILTER):
Dùng để tìm kiếm nick theo điều kiện. Logic: (Thỏa mãn nhóm AND) VÀ (Thỏa mãn nhóm OR)

{
    "and": {  // Nhóm AND: Nick phải thỏa mãn TẤT CẢ điều kiện trong này
        "match_col_3": ["US", "UK"],  // Cột 3 phải là US hoặc UK
        "min_col_10": 1000            // VÀ Cột 10 phải >= 1000
    },
    "or": {   // Nhóm OR: Nick chỉ cần thỏa mãn ÍT NHẤT MỘT điều kiện trong này
        "contains_col_5": "vip",      // Cột 5 chứa "vip"
        "max_col_6": 50               // HOẶC Cột 6 <= 50
    }
}

Lưu ý:
- match/contains: Nhận string "val" hoặc mảng ["val1", "val2"] (Logic OR trong mảng).
- min/max/last_hours: Nhận số (100) hoặc string ("100").
=================================================================================================
*/

// =================================================================================================
// 🟢 CẤU TRÚC DỮ LIỆU (STRUCTS)
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

// PriorityStep: Định nghĩa một bước tìm kiếm trong quy trình ưu tiên
type PriorityStep struct {
	Status  string // Trạng thái cần tìm (vd: "đang chạy")
	IsMy    bool   // true: Tìm nick đã gán cho mình. false: Tìm nick chung/trống.
	IsEmpty bool   // true: Tìm nick chưa có DeviceId.
	PrioID  int    // Độ ưu tiên (1 cao nhất). Dùng để log hoặc debug.
}

// CriteriaSet: Tập hợp các điều kiện lọc (Dùng chung cho cả nhóm AND và OR)
type CriteriaSet struct {
	MatchCols    map[int][]string // Map[IndexCột] -> Danh sách giá trị chấp nhận
	ContainsCols map[int][]string // Map[IndexCột] -> Danh sách từ khóa
	MinCols      map[int]float64  // Map[IndexCột] -> Giá trị tối thiểu
	MaxCols      map[int]float64  // Map[IndexCột] -> Giá trị tối đa
	TimeCols     map[int]float64  // Map[IndexCột] -> Số giờ trôi qua tối đa
	IsEmpty      bool             // Đánh dấu tập này có dữ liệu hay không
}

// FilterParams: Cấu trúc chứa toàn bộ yêu cầu lọc từ Client
type FilterParams struct {
	AndCriteria CriteriaSet // Nhóm điều kiện bắt buộc (AND)
	OrCriteria  CriteriaSet // Nhóm điều kiện mở rộng (OR)
	HasFilter   bool        // Cờ báo hiệu có dùng lọc hay không
}

// =================================================================================================
// 🟢 HANDLER CHÍNH: TIẾP NHẬN & ĐIỀU PHỐI REQUEST
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON Body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}

	// 2. Xác thực Token từ Context (Middleware đã làm việc này)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		return // Dừng nếu không có quyền
	}

	sid := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])

	// 3. Xử lý cờ Reset (Chạy lại nick đã xong)
	// Tách logic này ra để không ảnh hưởng đến việc phân loại Type
	isReset := false
	if reqAction, _ := body["action"].(string); CleanString(reqAction) == "reset" {
		isReset = true
		body["is_reset"] = true // Đẩy lại vào body để truyền xuống các hàm con
	}

	// 4. Phân loại Action (Hành động) chuẩn xác
	action := "login" // Mặc định

	if reqType == "view" {
		action = "view_only"
	} else if reqType == "register" {
		action = "register"
		// Register KHÔNG hỗ trợ Reset (Không tìm nick Completed)
	} else if reqType == "auto" {
		action = "auto"
	} else {
		// Nhóm Login
		if isReset {
			action = "login_reset" // Kích hoạt tìm kiếm nick Completed
		} else {
			action = "login"
		}
	}

	// 5. Gọi hàm xử lý nghiệp vụ chính
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)

	// 6. Trả về kết quả
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 CORE LOGIC: TÌM KIẾM VÀ XỬ LÝ DỮ LIỆU
// =================================================================================================

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	// 1. Tải dữ liệu từ Cache RAM (Tốc độ cao)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}

	// 2. Parse Row Index (Nếu client chỉ định đích danh)
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			rowIndexInput = int(val)
		}
	}

	// 3. Parse Bộ lọc Nâng cao (AND/OR Logic)
	filters := parseFilterParams(body)

	// Bắt đầu vùng Lock Đọc (Cho phép nhiều người đọc cùng lúc)
	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues)

	// ---------------------------------------------------------------------------------------------
	// 📍 NHÁNH 1: ƯU TIÊN TUYỆT ĐỐI (ROW INDEX)
	// Tìm đúng dòng chỉ định, kiểm tra điều kiện và trả về.
	// ---------------------------------------------------------------------------------------------
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < rawLen {
			cleanRow := cacheData.CleanValues[idx]
			row := cacheData.RawValues[idx]

			// Kiểm tra xem dòng này có thỏa mãn bộ lọc không (Nếu có lọc)
			if filters.HasFilter {
				if !isRowMatched(cleanRow, row, filters) {
					STATE.SheetMutex.RUnlock()
					return nil, fmt.Errorf("row_index không đủ điều kiện")
				}
			}

			// Kiểm tra chất lượng (Đủ user/pass/mail...)
			val := KiemTraChatLuongClean(cleanRow, action)
			if val.Valid {
				STATE.SheetMutex.RUnlock()
				// Row Index chỉ định chấp nhận ghi đè, không cần Double Check
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cleanRow), val.SystemEmail, action, 0)
			} else {
				STATE.SheetMutex.RUnlock()
				return nil, fmt.Errorf("row_index tài khoản lỗi: %s", val.Missing)
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Dòng yêu cầu không tồn tại")
	}

	// ---------------------------------------------------------------------------------------------
	// 📍 NHÁNH 2: TÌM KIẾM NÂNG CAO (ADVANCED FILTER)
	// Quét toàn bộ danh sách để tìm nick khớp điều kiện AND/OR.
	// ---------------------------------------------------------------------------------------------
	if filters.HasFilter {
		for i, cleanRow := range cacheData.CleanValues {
			// B1: Kiểm tra Dữ liệu (Fail Fast - Sai là bỏ qua ngay để tiết kiệm CPU)
			if !isRowMatched(cleanRow, cacheData.RawValues[i], filters) {
				continue
			}

			// B2: CHỐT CHẶN TRẠNG THÁI (Status Guard) - Quan trọng!
			// Ngăn chặn việc type="register" lấy nhầm nick đang nuôi Login và ngược lại.
			if !checkStatusIsValid(cleanRow[INDEX_DATA_TIKTOK.STATUS], action) {
				continue
			}

			// B3: Kiểm tra Quyền sở hữu (Của mình hoặc Trống)
			curDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
			if curDev != "" && curDev != deviceId {
				continue
			}

			// B4: Kiểm tra Chất lượng Nick
			val := KiemTraChatLuongClean(cleanRow, action)
			if val.Valid {
				// --- 🛡️ DOUBLE CHECK LOCKING (Fix Race Condition) ---
				// Nhả khóa đọc -> Khóa ghi để đảm bảo không ai tranh mất nick này trong tích tắc
				STATE.SheetMutex.RUnlock()
				STATE.SheetMutex.Lock()

				// Kiểm tra lại các điều kiện dễ biến động (Owner & Status)
				currCleanRow := cacheData.CleanValues[i]
				currDev := currCleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
				currStatus := currCleanRow[INDEX_DATA_TIKTOK.STATUS]

				// Nếu nick vẫn ngon (Chưa ai lấy & Trạng thái vẫn đúng) -> CHỐT ĐƠN
				if (currDev == "" || currDev == deviceId) && checkStatusIsValid(currStatus, action) {
					// Gán sở hữu ngay trong RAM để giữ chỗ
					cacheData.CleanValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
					cacheData.RawValues[i][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
					cacheData.AssignedMap[deviceId] = i

					STATE.SheetMutex.Unlock()
					return commit_and_response(sid, deviceId, cacheData, i, determineType(currCleanRow), val.SystemEmail, action, 0)
				}

				// Nếu bị tranh chấp -> Mở khóa ghi, quay lại khóa đọc để tìm dòng tiếp theo
				STATE.SheetMutex.Unlock()
				STATE.SheetMutex.RLock()
				// --- 🛡️ END DOUBLE CHECK ---
			} else {
				// Nick lỗi -> Tự sửa (Self Healing) và tìm tiếp
				STATE.SheetMutex.RUnlock()
				doSelfHealing(sid, i, val.Missing, cacheData)
				STATE.SheetMutex.RLock()
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo điều kiện")
	}

	// ---------------------------------------------------------------------------------------------
	// 📍 NHÁNH 3: TỰ ĐỘNG (AUTO / PRIORITY)
	// Chạy khi không có điều kiện lọc. Tìm theo thứ tự ưu tiên định sẵn.
	// ---------------------------------------------------------------------------------------------
	if action != "view_only" {
		isReset := false
		if v, ok := body["is_reset"].(bool); ok && v {
			isReset = true
		}
		if action == "login_reset" {
			isReset = true
		}

		// Lấy danh sách các bước cần tìm (VD: Tìm nick đang chạy trước, rồi mới tìm nick mới)
		steps := buildPrioritySteps(action, isReset)

		for _, step := range steps {
			// Lấy danh sách index từ Map trạng thái (O(1) - Rất nhanh)
			indices := cacheData.StatusMap[step.Status]

			for _, idx := range indices {
				if idx < rawLen {
					row := cacheData.CleanValues[idx]
					curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
					isMyNick := (curDev == deviceId)
					isEmptyNick := (curDev == "")

					// Kiểm tra sở hữu
					if (step.IsMy && isMyNick) || (step.IsEmpty && isEmptyNick) {
						// Kiểm tra chất lượng
						val := KiemTraChatLuongClean(row, action)
						if !val.Valid {
							STATE.SheetMutex.RUnlock()
							doSelfHealing(sid, idx, val.Missing, cacheData)
							STATE.SheetMutex.RLock()
							continue
						}

						// Double Check và Claim
						STATE.SheetMutex.RUnlock()
						STATE.SheetMutex.Lock()

						currentRealDev := cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
						if (step.IsMy && currentRealDev == deviceId) || (step.IsEmpty && currentRealDev == "") {
							// Giữ chỗ
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

	// Logic cuối cùng: Kiểm tra xem đã hoàn thành hết chưa để báo lỗi chuẩn
	checkList := []string{"login", "auto", "login_reset", "register"}
	isCheck := false
	for _, s := range checkList {
		if strings.Contains(action, s) {
			isCheck = true
			break
		}
	}

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

// =================================================================================================
// 🛠 CÁC HÀM HỖ TRỢ BỘ LỌC (FILTER HELPERS)
// =================================================================================================

// Hàm parse đệ quy các block "and", "or" trong JSON
func parseCriteriaSet(input interface{}) CriteriaSet {
	c := CriteriaSet{
		MatchCols: make(map[int][]string), ContainsCols: make(map[int][]string),
		MinCols: make(map[int]float64), MaxCols: make(map[int]float64), TimeCols: make(map[int]float64),
		IsEmpty: true,
	}

	data, ok := input.(map[string]interface{})
	if !ok {
		return c
	}

	for k, v := range data {
		if strings.HasPrefix(k, "match_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "match_col_")); err == nil {
				c.MatchCols[idx] = ToSlice(v); c.IsEmpty = false
			}
		} else if strings.HasPrefix(k, "contains_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "contains_col_")); err == nil {
				c.ContainsCols[idx] = ToSlice(v); c.IsEmpty = false
			}
		} else if strings.HasPrefix(k, "min_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "min_col_")); err == nil {
				if val, ok := toFloat(v); ok { c.MinCols[idx] = val; c.IsEmpty = false }
			}
		} else if strings.HasPrefix(k, "max_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "max_col_")); err == nil {
				if val, ok := toFloat(v); ok { c.MaxCols[idx] = val; c.IsEmpty = false }
			}
		} else if strings.HasPrefix(k, "last_hours_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "last_hours_col_")); err == nil {
				if val, ok := toFloat(v); ok { c.TimeCols[idx] = val; c.IsEmpty = false }
			}
		} else if strings.HasPrefix(k, "search_col_") { // Legacy support
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "search_col_")); err == nil {
				c.MatchCols[idx] = ToSlice(v); c.IsEmpty = false
			}
		}
	}
	return c
}

// Hàm tổng hợp FilterParams từ Body
func parseFilterParams(body map[string]interface{}) FilterParams {
	f := FilterParams{
		HasFilter: false,
	}

	// 1. Parse nhóm AND (Mặc định các key ở root level cũng tính là AND để hỗ trợ Legacy)
	f.AndCriteria = parseCriteriaSet(body)
	if v, ok := body["and"]; ok {
		// Nếu có key "and" riêng, merge thêm vào (hoặc ghi đè tùy logic, ở đây ta parse riêng)
		// Để đơn giản và chuẩn xác, ta nên ưu tiên parse từ key "and" nếu nó tồn tại
		subAnd := parseCriteriaSet(v)
		if !subAnd.IsEmpty {
			// Merge logic (đơn giản là copy đè vì struct map reference)
			for k, v := range subAnd.MatchCols { f.AndCriteria.MatchCols[k] = v }
			for k, v := range subAnd.ContainsCols { f.AndCriteria.ContainsCols[k] = v }
			for k, v := range subAnd.MinCols { f.AndCriteria.MinCols[k] = v }
			for k, v := range subAnd.MaxCols { f.AndCriteria.MaxCols[k] = v }
			for k, v := range subAnd.TimeCols { f.AndCriteria.TimeCols[k] = v }
			f.AndCriteria.IsEmpty = false
		}
	}

	// 2. Parse nhóm OR
	if v, ok := body["or"]; ok {
		f.OrCriteria = parseCriteriaSet(v)
	}

	if !f.AndCriteria.IsEmpty || !f.OrCriteria.IsEmpty {
		f.HasFilter = true
	}
	return f
}

// Hàm kiểm tra một dòng có khớp với bộ tiêu chí (CriteriaSet) hay không
// modeMatchAll: true (cho nhóm AND), false (cho nhóm OR - chỉ cần 1 cái đúng)
func checkCriteriaMatch(cleanRow []string, rawRow []interface{}, c CriteriaSet, modeMatchAll bool) bool {
	if c.IsEmpty {
		return true // Nếu không có tiêu chí gì thì coi như khớp
	}

	// Hàm helper để xử lý kết quả từng điều kiện
	// Nếu mode là AND: Gặp sai -> return false ngay.
	// Nếu mode là OR: Gặp đúng -> return true ngay.
	processResult := func(isMatch bool) (bool, bool) { // (FinalResult, ShouldReturnNow)
		if modeMatchAll {
			if !isMatch { return false, true } // AND: Sai là chết
		} else {
			if isMatch { return true, true } // OR: Đúng là ăn
		}
		return false, false // Tiếp tục kiểm tra
	}

	// 1. Kiểm tra Match
	for idx, targets := range c.MatchCols {
		cellVal := ""; if idx < len(cleanRow) { cellVal = cleanRow[idx] }
		match := false; for _, t := range targets { if t == cellVal { match = true; break } }
		if res, stop := processResult(match); stop { return res }
	}

	// 2. Kiểm tra Contains
	for idx, targets := range c.ContainsCols {
		cellVal := ""; if idx < len(cleanRow) { cellVal = cleanRow[idx] }
		match := false; for _, t := range targets { if t == "" { if cellVal == "" { match = true; break } } else { if strings.Contains(cellVal, t) { match = true; break } } }
		if res, stop := processResult(match); stop { return res }
	}

	// 3. Kiểm tra Số học (Min/Max)
	for idx, minVal := range c.MinCols {
		val, ok := getFloatVal(rawRow, idx)
		match := ok && val >= minVal
		if res, stop := processResult(match); stop { return res }
	}
	for idx, maxVal := range c.MaxCols {
		val, ok := getFloatVal(rawRow, idx)
		match := ok && val <= maxVal
		if res, stop := processResult(match); stop { return res }
	}

	// 4. Kiểm tra Thời gian
	now := time.Now().UnixMilli()
	for idx, hours := range c.TimeCols {
		timeVal := int64(0); if idx < len(rawRow) { timeVal = ConvertSerialDate(rawRow[idx]) }
		match := timeVal > 0 && (float64(now-timeVal)/3600000.0 <= hours)
		if res, stop := processResult(match); stop { return res }
	}

	// Kết quả mặc định khi chạy hết vòng lặp mà chưa return
	if modeMatchAll {
		return true // AND: Chạy hết mà không sai cái nào -> Đúng
	} else {
		return false // OR: Chạy hết mà không đúng cái nào -> Sai
	}
}

// Hàm kiểm tra tổng hợp: (Thỏa mãn AND) VÀ (Thỏa mãn OR)
func isRowMatched(cleanRow []string, rawRow []interface{}, f FilterParams) bool {
	// 1. Kiểm tra nhóm AND (Bắt buộc tất cả phải đúng)
	if !f.AndCriteria.IsEmpty {
		if !checkCriteriaMatch(cleanRow, rawRow, f.AndCriteria, true) {
			return false
		}
	}

	// 2. Kiểm tra nhóm OR (Nếu có, phải thỏa mãn ít nhất 1 cái)
	if !f.OrCriteria.IsEmpty {
		if !checkCriteriaMatch(cleanRow, rawRow, f.OrCriteria, false) {
			return false
		}
	}

	return true
}

// =================================================================================================
// 🛠 CÁC HÀM HỖ TRỢ KHÁC (STATUS, PRIORITY, CLEANUP)
// =================================================================================================

// Hàm kiểm tra trạng thái có hợp lệ với Action không (Status Guard)
func checkStatusIsValid(currentStatus, action string) bool {
	if action == "register" {
		// Register chỉ nhận: đăng ký, đang đăng ký, chờ đăng ký
		if currentStatus == STATUS_READ.REGISTER || currentStatus == STATUS_READ.REGISTERING || currentStatus == STATUS_READ.WAIT_REG {
			return true
		}
	} else if action == "login" || action == "login_reset" {
		// Login nhận: đăng nhập, đang chạy, đang chờ
		if currentStatus == STATUS_READ.LOGIN || currentStatus == STATUS_READ.RUNNING || currentStatus == STATUS_READ.WAITING {
			return true
		}
		// Reset Login nhận thêm: hoàn thành
		if (action == "login_reset") && currentStatus == STATUS_READ.COMPLETED {
			return true
		}
	} else if action == "auto" {
		return true // Auto chấp nhận tất cả
	} else {
		return true // View only
	}
	return false
}

func buildPrioritySteps(action string, isReset bool) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	if strings.Contains(action, "login") {
		add(STATUS_READ.RUNNING, true, false, 1); add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3); add(STATUS_READ.LOGIN, false, true, 4)
		if isReset { add(STATUS_READ.COMPLETED, true, false, 5) }
	} else if action == "register" {
		add(STATUS_READ.REGISTERING, true, false, 1); add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3); add(STATUS_READ.REGISTER, false, true, 4)
	} else if action == "auto" {
		add(STATUS_READ.RUNNING, true, false, 1); add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3); add(STATUS_READ.LOGIN, false, true, 4)
		add(STATUS_READ.REGISTERING, true, false, 5); add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7); add(STATUS_READ.REGISTER, false, true, 8)
		if isReset { add(STATUS_READ.COMPLETED, true, false, 9) }
	}
	return steps
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
	if typ == "register" { tSt = STATUS_WRITE.REGISTERING }

	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false
	if (strings.Contains(action, "auto") || strings.Contains(action, "login_reset")) && (priority == 5 || priority == 9) {
		mode = "reset"; isResetCompleted = true
	}
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)
	for _, cIdx := range cleanupIndices {
		cSt := STATUS_WRITE.WAITING
		if typ == "register" { cSt = STATUS_WRITE.WAIT_REG }
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		cNote := tao_ghi_chu_chuan(cOldNote, cSt, "normal")
		if isResetCompleted { cNote = tao_ghi_chu_chuan(cOldNote, "Reset chờ chạy", "reset") }

		oldCSt := cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS]
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = cSt
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = cNote
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = CleanString(cSt) }
		if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT { cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = CleanString(cNote) }
		if oldCSt != CleanString(cSt) {
			removeFromStatusMap(cache.StatusMap, oldCSt, cIdx)
			newCSt := CleanString(cSt)
			cache.StatusMap[newCSt] = append(cache.StatusMap[newCSt], cIdx)
		}
		cRow := make([]interface{}, len(cache.RawValues[cIdx]))
		copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

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
	STATE.SheetMutex.Unlock()

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
		for i, v := range list { if v == targetIdx { m[status] = append(list[:i], list[i+1:]...); return } }
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
	fullRow := make([]interface{}, len(cache.RawValues[idx])); copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()
	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

func tao_ghi_chu_chuan(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	if mode == "new" { return fmt.Sprintf("%s\n%s", newStatus, nowFull) }
	count := 0; oldNote = strings.TrimSpace(oldNote); lines := strings.Split(oldNote, "\n")
	if idx := strings.Index(oldNote, "(Lần"); idx != -1 {
		end := strings.Index(oldNote[idx:], ")")
		if end != -1 { fmt.Sscanf(oldNote[idx+len("(Lần"):idx+end], "%d", &count) }
	}
	if count == 0 { count = 1 }
	today := nowFull[:10]; oldDate := ""
	for _, l := range lines { if len(l) >= 10 && strings.Contains(l, "/") { oldDate = l[:10]; break } }
	if oldDate != today { count = 1 } else {
		if mode == "reset" { count++ } else if count == 0 { count = 1 }
	}
	st := newStatus; if st == "" && len(lines) > 0 { st = lines[0] }
	if st == "" { st = "Đang chạy" }
	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
