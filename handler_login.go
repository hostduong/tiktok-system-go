package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// =================================================================================================
// 🟢 CẤU TRÚC DỮ LIỆU RESPONSE (PHẢN HỒI JSON)
// =================================================================================================

type LoginResponse struct {
	Status          string          `json:"status"`           // Trạng thái phản hồi (true/false)
	Type            string          `json:"type"`             // Loại hành động (login/register)
	Messenger       string          `json:"messenger"`        // Thông báo chi tiết
	DeviceId        string          `json:"deviceId"`         // ID thiết bị
	RowIndex        int             `json:"row_index"`        // Dòng dữ liệu trong Excel
	SystemEmail     string          `json:"system_email"`     // Email hệ thống (nếu có)
	AuthProfile     AuthProfile     `json:"auth_profile"`     // Thông tin đăng nhập
	ActivityProfile ActivityProfile `json:"activity_profile"` // Thông tin hoạt động
	AiProfile       AiProfile       `json:"ai_profile"`       // Thông tin AI nuôi nick
}

// Cấu trúc định nghĩa một bước ưu tiên tìm kiếm
type PriorityStep struct {
	Status  string // Trạng thái cần tìm (ví dụ: "đang chạy")
	IsMy    bool   // Tìm nick của mình? (true/false)
	IsEmpty bool   // Tìm nick chưa ai nhận? (true/false)
	PrioID  int    // Độ ưu tiên (số càng nhỏ càng ưu tiên cao)
}

// Cấu trúc chứa các tham số lọc nâng cao từ Client
type FilterParams struct {
	MatchCols    map[int][]string // Cột phải khớp chính xác (Match)
	ContainsCols map[int][]string // Cột phải chứa từ khóa (Contains)
	MinCols      map[int]float64  // Cột có giá trị >= Min
	MaxCols      map[int]float64  // Cột có giá trị <= Max
	TimeCols     map[int]float64  // Cột thời gian trong khoảng X giờ gần nhất
	HasFilter    bool             // Cờ đánh dấu có dùng bộ lọc hay không
}

// =================================================================================================
// 🟢 HANDLER CHÍNH: TIẾP NHẬN REQUEST & PHÂN LOẠI
// =================================================================================================

func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	// 1. Đọc dữ liệu JSON từ Body request
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	// 2. Lấy thông tin Token từ Context (đã xác thực ở Middleware)
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		return // Nếu không có token, dừng xử lý (Middleware đã chặn rồi)
	}

	sid := tokenData.SpreadsheetID           // ID file Google Sheet
	deviceId := CleanString(body["deviceId"]) // ID thiết bị của client
	reqType := CleanString(body["type"])      // Loại request: register, login, auto, view...

	// 3. Xử lý Logic Reset (Chạy lại nick đã xong)
	isReset := false
	if reqAction, _ := body["action"].(string); CleanString(reqAction) == "reset" {
		isReset = true
		body["is_reset"] = true // Gắn cờ vào body để truyền xuống các hàm con
	}

	// 4. Phân loại Action (Hành động) chuẩn xác
	action := "login" // Mặc định là Login

	if reqType == "view" {
		action = "view_only" // Chỉ xem, không sửa đổi
	} else if reqType == "register" {
		action = "register"
		// LƯU Ý: Với Register, action="reset" là VÔ TÁC DỤNG (Không tìm nick Completed)
	} else if reqType == "auto" {
		action = "auto" // Tự động thông minh
	} else {
		// Trường hợp Login (hoặc reqType rỗng)
		if isReset {
			action = "login_reset" // Login có kèm chạy lại nick cũ
		} else {
			action = "login"
		}
	}

	// 5. Gọi hàm xử lý cốt lõi để lấy nick
	res, err := xu_ly_lay_du_lieu(sid, deviceId, body, action)

	// 6. Trả về kết quả cho Client
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		// Nếu có lỗi -> Trả về status: false + nội dung lỗi
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}
	// Nếu thành công -> Trả về LoginResponse
	json.NewEncoder(w).Encode(res)
}

// =================================================================================================
// 🟢 LOGIC LÕI: TÌM KIẾM DỮ LIỆU THEO 3 NHÁNH (STRATEGY PATTERN)
// =================================================================================================

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	// 1. Tải dữ liệu từ Cache RAM (Sheet: DataTiktok)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil {
		return nil, fmt.Errorf("Lỗi tải dữ liệu")
	}

	// 2. Parse chỉ số dòng (nếu client chỉ định row_index)
	rowIndexInput := -1
	if v, ok := body["row_index"]; ok {
		if val, ok := toFloat(v); ok {
			rowIndexInput = int(val)
		}
	}

	// 3. Parse các tham số bộ lọc nâng cao (nếu có)
	filters := parseFilterParams(body)

	// Khóa Cache để đọc an toàn (Read Lock)
	STATE.SheetMutex.RLock()
	rawLen := len(cacheData.RawValues) // Tổng số dòng dữ liệu

	// =================================================================================
	// 🟢 NHÁNH 1: PRIORITY TUYỆT ĐỐI (Khi có row_index)
	// =================================================================================
	if rowIndexInput >= RANGES.DATA_START_ROW {
		idx := rowIndexInput - RANGES.DATA_START_ROW
		if idx >= 0 && idx < rawLen {
			cleanRow := cacheData.CleanValues[idx] // Dữ liệu đã chuẩn hóa (lowercase)
			row := cacheData.RawValues[idx]        // Dữ liệu gốc

			// B1: Kiểm tra bộ lọc (Nếu có filter thì dòng này phải thỏa mãn)
			if filters.HasFilter {
				if !isRowMatched(cleanRow, row, filters) {
					STATE.SheetMutex.RUnlock()
					return nil, fmt.Errorf("row_index không đủ điều kiện")
				}
			}

			// B2: Kiểm tra chất lượng nick (Có đủ user/pass/email theo action không?)
			val := KiemTraChatLuongClean(cleanRow, action)
			if val.Valid {
				// Ngon -> Mở khóa đọc, thực hiện Ghi nhận (Commit)
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, idx, determineType(cleanRow), val.SystemEmail, action, 0)
			} else {
				// Hỏng -> Báo lỗi cụ thể
				STATE.SheetMutex.RUnlock()
				return nil, fmt.Errorf("row_index tài khoản lỗi: %s", val.Missing)
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Dòng yêu cầu không tồn tại")
	}

	// =================================================================================
	// 🟢 NHÁNH 2: TÌM KIẾM NÂNG CAO (Khi có Filters - match_col, min_col...)
	// =================================================================================
	if filters.HasFilter {
		// Duyệt từng dòng từ trên xuống dưới
		for i, cleanRow := range cacheData.CleanValues {
			// B1: Kiểm tra điều kiện lọc (Fail Fast - Sai là bỏ qua ngay)
			if !isRowMatched(cleanRow, cacheData.RawValues[i], filters) {
				continue
			}

			// B2: CHỐT CHẶN TRẠNG THÁI (Status Guard) - Logic quan trọng mới thêm
			currentStatus := cleanRow[INDEX_DATA_TIKTOK.STATUS]
			isValidStatus := false

			if action == "register" {
				// Register chỉ nhận: đăng ký, đang đăng ký, chờ đăng ký
				if currentStatus == STATUS_READ.REGISTER || currentStatus == STATUS_READ.REGISTERING || currentStatus == STATUS_READ.WAIT_REG {
					isValidStatus = true
				}
			} else if action == "login" || action == "login_reset" {
				// Login nhận: đăng nhập, đang chạy, đang chờ
				if currentStatus == STATUS_READ.LOGIN || currentStatus == STATUS_READ.RUNNING || currentStatus == STATUS_READ.WAITING {
					isValidStatus = true
				}
				// Nếu có Reset -> Nhận thêm Hoàn thành
				if !isValidStatus && (action == "login_reset") && currentStatus == STATUS_READ.COMPLETED {
					isValidStatus = true
				}
			} else if action == "auto" {
				// Auto nhận tất cả (trừ Hoàn thành nếu không reset)
				// Code đơn giản hóa: Auto chấp nhận nếu nó không phải là Rác/Lỗi
				// (Logic auto sẽ lọc kỹ hơn ở bước Quality, ở đây tạm cho qua để linh hoạt)
				isValidStatus = true
			} else {
				// View Only hoặc trường hợp khác -> Cho qua
				isValidStatus = true
			}

			// Nếu trạng thái không hợp lệ với Action -> Bỏ qua dòng này
			if !isValidStatus {
				continue
			}

			// B3: Kiểm tra quyền sở hữu (Của mình hoặc Trống)
			curDev := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID]
			if curDev != "" && curDev != deviceId {
				continue
			}

			// B4: Kiểm tra chất lượng nick
			val := KiemTraChatLuongClean(cleanRow, action)
			if val.Valid {
				// Ngon -> Lấy luôn
				STATE.SheetMutex.RUnlock()
				return commit_and_response(sid, deviceId, cacheData, i, determineType(cleanRow), val.SystemEmail, action, 0)
			} else {
				// Khớp Filter + Của mình nhưng nick Hỏng -> Tự động sửa (Self Healing)
				STATE.SheetMutex.RUnlock()
				doSelfHealing(sid, i, val.Missing, cacheData)
				STATE.SheetMutex.RLock() // Khóa lại để chạy tiếp vòng lặp
			}
		}
		STATE.SheetMutex.RUnlock()
		return nil, fmt.Errorf("Không tìm thấy tài khoản theo điều kiện")
	}

	// =================================================================================
	// 🟢 NHÁNH 3: TỰ ĐỘNG MẶC ĐỊNH (Khi không row_index, không filters)
	// =================================================================================
	if action != "view_only" {
		isReset := false
		if v, ok := body["is_reset"].(bool); ok && v {
			isReset = true
		}
		if action == "login_reset" {
			isReset = true
		}

		// Xây dựng danh sách các bước ưu tiên (VD: Tìm nick đang chạy trước, rồi mới tìm nick mới)
		steps := buildPrioritySteps(action, isReset)

		for _, step := range steps {
			// Lấy danh sách index của các nick có trạng thái tương ứng (Tra map O(1) cực nhanh)
			indices := cacheData.StatusMap[step.Status]

			for _, idx := range indices {
				if idx < rawLen {
					row := cacheData.CleanValues[idx]
					curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]

					isMyNick := (curDev == deviceId)
					isEmptyNick := (curDev == "")

					// Kiểm tra sở hữu theo cấu hình của bước hiện tại
					if (step.IsMy && isMyNick) || (step.IsEmpty && isEmptyNick) {
						// Kiểm tra chất lượng
						val := KiemTraChatLuongClean(row, action)

						if !val.Valid {
							// Nick hỏng -> Tự sửa và bỏ qua
							STATE.SheetMutex.RUnlock()
							doSelfHealing(sid, idx, val.Missing, cacheData)
							STATE.SheetMutex.RLock()
							continue
						}

						// BẮT ĐẦU QUÁ TRÌNH "CLAIM" (CHIẾM HỮU NICK)
						STATE.SheetMutex.RUnlock()
						STATE.SheetMutex.Lock() // Chuyển sang khóa Ghi (Write Lock)

						// Double Check (Kiểm tra lại lần nữa sau khi lock để tránh race condition)
						currentRealDev := cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID]
						if (step.IsMy && currentRealDev == deviceId) || (step.IsEmpty && currentRealDev == "") {
							// Gán ngay DeviceID vào RAM
							cacheData.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
							cacheData.AssignedMap[deviceId] = idx

							STATE.SheetMutex.Unlock()
							// Thực hiện cam kết và trả về
							return commit_and_response(sid, deviceId, cacheData, idx, determineType(cacheData.CleanValues[idx]), val.SystemEmail, action, step.PrioID)
						}

						// Nếu bị tranh chấp (người khác lấy mất trong mili giây) -> Mở khóa và tìm tiếp
						STATE.SheetMutex.Unlock()
						STATE.SheetMutex.RLock()
					}
				}
			}
		}
	}

	// Logic báo lỗi cuối cùng: Kiểm tra xem user có nick nào đã hoàn thành không
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

// ------------------------------------------------------------------------------------------------
// 🛠 BỘ HÀM HỖ TRỢ FILTER (ĐÃ FIX getFloatVal)
// ------------------------------------------------------------------------------------------------

func parseFilterParams(body map[string]interface{}) FilterParams {
	f := FilterParams{
		MatchCols:    make(map[int][]string),
		ContainsCols: make(map[int][]string),
		MinCols:      make(map[int]float64),
		MaxCols:      make(map[int]float64),
		TimeCols:     make(map[int]float64),
		HasFilter:    false,
	}

	for k, v := range body {
		// Duyệt qua các key của JSON body để tìm điều kiện lọc
		if strings.HasPrefix(k, "match_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "match_col_")); err == nil {
				f.MatchCols[idx] = ToSlice(v)
				f.HasFilter = true
			}
		} else if strings.HasPrefix(k, "contains_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "contains_col_")); err == nil {
				f.ContainsCols[idx] = ToSlice(v)
				f.HasFilter = true
			}
		} else if strings.HasPrefix(k, "min_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "min_col_")); err == nil {
				if val, ok := toFloat(v); ok {
					f.MinCols[idx] = val
					f.HasFilter = true
				}
			}
		} else if strings.HasPrefix(k, "max_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "max_col_")); err == nil {
				if val, ok := toFloat(v); ok {
					f.MaxCols[idx] = val
					f.HasFilter = true
				}
			}
		} else if strings.HasPrefix(k, "last_hours_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "last_hours_col_")); err == nil {
				if val, ok := toFloat(v); ok {
					f.TimeCols[idx] = val
					f.HasFilter = true
				}
			}
		} else if strings.HasPrefix(k, "search_col_") {
			// Hỗ trợ legacy key
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "search_col_")); err == nil {
				f.MatchCols[idx] = ToSlice(v)
				f.HasFilter = true
			}
		}
	}
	return f
}

func isRowMatched(cleanRow []string, rawRow []interface{}, f FilterParams) bool {
	// 1. Kiểm tra Match (So khớp chính xác)
	for idx, targets := range f.MatchCols {
		cellVal := ""
		if idx < len(cleanRow) {
			cellVal = cleanRow[idx]
		}
		match := false
		for _, t := range targets {
			if t == cellVal {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	// 2. Kiểm tra Contains (So khớp chứa)
	for idx, targets := range f.ContainsCols {
		cellVal := ""
		if idx < len(cleanRow) {
			cellVal = cleanRow[idx]
		}
		match := false
		for _, t := range targets {
			if t == "" {
				if cellVal == "" {
					match = true
					break
				}
			} else {
				if strings.Contains(cellVal, t) {
					match = true
					break
				}
			}
		}
		if !match {
			return false
		}
	}

	// 3. Kiểm tra Min/Max (So sánh số học)
	// Sử dụng getFloatVal(row, idx) với 2 tham số -> Fix lỗi build
	for idx, minVal := range f.MinCols {
		if val, ok := getFloatVal(rawRow, idx); !ok || val < minVal {
			return false
		}
	}
	for idx, maxVal := range f.MaxCols {
		if val, ok := getFloatVal(rawRow, idx); !ok || val > maxVal {
			return false
		}
	}

	// 4. Kiểm tra Time (Thời gian trôi qua)
	now := time.Now().UnixMilli()
	for idx, hours := range f.TimeCols {
		timeVal := int64(0)
		if idx < len(rawRow) {
			timeVal = ConvertSerialDate(rawRow[idx])
		}
		if timeVal == 0 {
			return false
		}
		// Tính khoảng cách thời gian theo giờ
		if float64(now-timeVal)/3600000.0 > hours {
			return false
		}
	}

	return true
}

// ------------------------------------------------------------------------------------------------
// 🟢 CÁC HÀM LOGIC ƯU TIÊN VÀ XỬ LÝ
// ------------------------------------------------------------------------------------------------

func buildPrioritySteps(action string, isReset bool) []PriorityStep {
	steps := make([]PriorityStep, 0, 10)
	// Hàm helper để thêm bước ưu tiên gọn gàng
	add := func(st string, my, empty bool, prio int) {
		steps = append(steps, PriorityStep{Status: st, IsMy: my, IsEmpty: empty, PrioID: prio})
	}

	if strings.Contains(action, "login") {
		// Luồng Login: Ưu tiên Đang chạy -> Đang chờ -> Login gốc
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		if isReset {
			add(STATUS_READ.COMPLETED, true, false, 5) // Nếu reset -> Tìm cả Completed
		}
	} else if action == "register" {
		// Luồng Register: Ưu tiên Đang đk -> Chờ đk -> Đăng ký gốc
		add(STATUS_READ.REGISTERING, true, false, 1)
		add(STATUS_READ.WAIT_REG, true, false, 2)
		add(STATUS_READ.REGISTER, true, false, 3)
		add(STATUS_READ.REGISTER, false, true, 4)
		// Register KHÔNG có logic reset Completed
	} else if action == "auto" {
		// Luồng Auto: Quét Login trước -> Hết Login mới quét Register
		add(STATUS_READ.RUNNING, true, false, 1)
		add(STATUS_READ.WAITING, true, false, 2)
		add(STATUS_READ.LOGIN, true, false, 3)
		add(STATUS_READ.LOGIN, false, true, 4)
		add(STATUS_READ.REGISTERING, true, false, 5)
		add(STATUS_READ.WAIT_REG, true, false, 6)
		add(STATUS_READ.REGISTER, true, false, 7)
		add(STATUS_READ.REGISTER, false, true, 8)
		if isReset {
			add(STATUS_READ.COMPLETED, true, false, 9) // Reset chỉ áp dụng cho nick login
		}
	}
	return steps
}

func determineType(row []string) string {
	// Xác định loại tài khoản dựa trên trạng thái hiện tại
	st := row[INDEX_DATA_TIKTOK.STATUS]
	if st == STATUS_READ.REGISTER || st == STATUS_READ.REGISTERING || st == STATUS_READ.WAIT_REG {
		return "register"
	}
	return "login"
}

func getCleanupIndices(cache *SheetCacheData, deviceId string, targetIdx int, isResetCompleted bool) []int {
	var list []int
	// Các trạng thái cần dọn dẹp: Đang chạy & Đang đăng ký
	checkList := []string{STATUS_READ.RUNNING, STATUS_READ.REGISTERING}
	
	// Nếu là Reset -> Cần dọn dẹp cả nick Completed (vì nick Completed đang được lôi ra chạy lại)
	if isResetCompleted {
		checkList = append(checkList, STATUS_READ.COMPLETED)
	}

	for _, st := range checkList {
		indices := cache.StatusMap[st]
		for _, idx := range indices {
			// Lấy nick cùng deviceId nhưng khác dòng hiện tại (targetIdx)
			if idx != targetIdx && idx < len(cache.CleanValues) {
				if cache.CleanValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] == deviceId {
					list = append(list, idx)
				}
			}
		}
	}
	return list
}

// 🔥 HÀM COMMIT VÀ TRẢ VỀ: ĐÃ CÓ LOGIC DỌN DẸP GIỮ LẠI SỐ LẦN CHẠY
func commit_and_response(sid, deviceId string, cache *SheetCacheData, idx int, typ, email, action string, priority int) (*LoginResponse, error) {
	// 1. Nếu chỉ xem -> Trả về luôn
	if action == "view_only" {
		row := cache.RawValues[idx]
		return &LoginResponse{
			Status: "true", Type: typ, Messenger: "OK", DeviceId: deviceId,
			RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
			AuthProfile: MakeAuthProfile(row), ActivityProfile: MakeActivityProfile(row), AiProfile: MakeAiProfile(row),
		}, nil
	}

	// 2. Chuẩn bị trạng thái đích (Target)
	row := cache.RawValues[idx]
	tSt := STATUS_WRITE.RUNNING // Mặc định là Đang chạy
	if typ == "register" {
		tSt = STATUS_WRITE.REGISTERING // Nếu là luồng Reg -> Đang đăng ký
	}

	oldNote := SafeString(row[INDEX_DATA_TIKTOK.NOTE])
	mode := "normal"
	isResetCompleted := false

	// Kiểm tra xem có phải là Reset nick Completed không (dựa vào PrioID)
	// Prio 5 (Login Reset), Prio 9 (Auto Reset)
	if (strings.Contains(action, "auto") || strings.Contains(action, "login_reset")) && (priority == 5 || priority == 9) {
		mode = "reset"
		isResetCompleted = true
	}

	// Tạo ghi chú cho nick MỚI
	tNote := tao_ghi_chu_chuan(oldNote, tSt, mode)

	STATE.SheetMutex.Lock()
	
	// --- XỬ LÝ DỌN DẸP (CLEANUP) CÁC NICK CŨ ---
	cleanupIndices := getCleanupIndices(cache, deviceId, idx, isResetCompleted)

	for _, cIdx := range cleanupIndices {
		// Xác định trạng thái chờ tương ứng
		cSt := STATUS_WRITE.WAITING // "Đang chờ"
		if typ == "register" {
			cSt = STATUS_WRITE.WAIT_REG // "Chờ đăng ký"
		}

		// LOGIC MỚI: Giữ lại thông tin lịch sử
		cOldNote := SafeString(cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE])
		
		// Dùng hàm chuẩn với mode "normal" để giữ nguyên count và cập nhật thời gian
		cNote := tao_ghi_chu_chuan(cOldNote, cSt, "normal")

		// Nếu là Reset, ghi chú đặc biệt
		if isResetCompleted {
			cNote = tao_ghi_chu_chuan(cOldNote, "Reset chờ chạy", "reset")
		}

		// Cập nhật vào Cache RAM cho dòng cũ
		oldCSt := cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS]
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = cSt
		cache.RawValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = cNote

		// Cập nhật bản Clean (tìm kiếm)
		if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
			cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.STATUS] = CleanString(cSt)
		}
		if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT {
			cache.CleanValues[cIdx][INDEX_DATA_TIKTOK.NOTE] = CleanString(cNote)
		}

		// Cập nhật StatusMap (Chuyển danh sách từ trạng thái cũ sang mới)
		if oldCSt != CleanString(cSt) {
			removeFromStatusMap(cache.StatusMap, oldCSt, cIdx)
			newCSt := CleanString(cSt)
			cache.StatusMap[newCSt] = append(cache.StatusMap[newCSt], cIdx)
		}

		// Đẩy xuống Queue để ghi đĩa sau
		cRow := make([]interface{}, len(cache.RawValues[cIdx]))
		copy(cRow, cache.RawValues[cIdx])
		go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, cIdx, cRow)
	}

	// --- CẬP NHẬT NICK ĐÍCH (TARGET) ---
	oldCleanSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
	
	cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = tSt       // Set Status mới
	cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = tNote       // Set Note mới
	cache.RawValues[idx][INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId // Set chủ sở hữu

	// Cập nhật bản Clean
	if INDEX_DATA_TIKTOK.STATUS < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(tSt)
	}
	if INDEX_DATA_TIKTOK.NOTE < CACHE.CLEAN_COL_LIMIT {
		cache.CleanValues[idx][INDEX_DATA_TIKTOK.NOTE] = CleanString(tNote)
	}

	// Cập nhật StatusMap
	if oldCleanSt != CleanString(tSt) {
		removeFromStatusMap(cache.StatusMap, oldCleanSt, idx)
		newSt := CleanString(tSt)
		cache.StatusMap[newSt] = append(cache.StatusMap[newSt], idx)
	}
	STATE.SheetMutex.Unlock()

	// Đẩy nick đích xuống Queue ghi
	newRow := make([]interface{}, len(row))
	copy(newRow, row)
	newRow[INDEX_DATA_TIKTOK.STATUS] = tSt
	newRow[INDEX_DATA_TIKTOK.NOTE] = tNote
	newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, newRow)

	// Chuẩn bị thông báo trả về
	msg := "Lấy nick đăng nhập thành công"
	if typ == "register" {
		msg = "Lấy nick đăng ký thành công"
	}

	return &LoginResponse{
		Status: "true", Type: typ, Messenger: msg, DeviceId: deviceId,
		RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: MakeAuthProfile(newRow), ActivityProfile: MakeActivityProfile(newRow), AiProfile: MakeAiProfile(newRow),
	}, nil
}

// Hàm xóa một index khỏi map trạng thái
func removeFromStatusMap(m map[string][]int, status string, targetIdx int) {
	if list, ok := m[status]; ok {
		for i, v := range list {
			if v == targetIdx {
				// Xóa phần tử tại vị trí i (cắt slice)
				m[status] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// Hàm tự sửa lỗi (Self Healing): Đánh dấu nick lỗi là Attention
func doSelfHealing(sid string, idx int, missing string, cache *SheetCacheData) {
	msg := "Nick thiếu " + missing + "\n" + time.Now().Format("02/01/2006 15:04:05")

	STATE.SheetMutex.Lock()
	if idx < len(cache.RawValues) {
		// Set trạng thái Chú ý
		cache.RawValues[idx][INDEX_DATA_TIKTOK.STATUS] = STATUS_WRITE.ATTENTION
		cache.RawValues[idx][INDEX_DATA_TIKTOK.NOTE] = msg
		
		// Update map trạng thái
		if idx < len(cache.CleanValues) && INDEX_DATA_TIKTOK.STATUS < len(cache.CleanValues[idx]) {
			oldSt := cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS]
			removeFromStatusMap(cache.StatusMap, oldSt, idx)
			cache.CleanValues[idx][INDEX_DATA_TIKTOK.STATUS] = CleanString(STATUS_WRITE.ATTENTION)
		}
	}
	// Copy row để ghi
	fullRow := make([]interface{}, len(cache.RawValues[idx]))
	copy(fullRow, cache.RawValues[idx])
	STATE.SheetMutex.Unlock()
	
	// Ghi đĩa
	go QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, idx, fullRow)
}

// Hàm tạo ghi chú chuẩn format: Trạng thái + Thời gian + (Lần x)
func tao_ghi_chu_chuan(oldNote, newStatus, mode string) string {
	nowFull := time.Now().Add(7 * time.Hour).Format("02/01/2006 15:04:05")
	
	// Nếu là nick mới hoàn toàn (append)
	if mode == "new" {
		return fmt.Sprintf("%s\n%s", newStatus, nowFull)
	}

	// Logic lấy số lần chạy từ note cũ
	count := 0
	oldNote = strings.TrimSpace(oldNote)
	lines := strings.Split(oldNote, "\n")
	
	// Parse chuỗi "(Lần x)"
	if idx := strings.Index(oldNote, "(Lần"); idx != -1 {
		end := strings.Index(oldNote[idx:], ")")
		if end != -1 {
			fmt.Sscanf(oldNote[idx+len("(Lần"):idx+end], "%d", &count)
		}
	}
	if count == 0 {
		count = 1
	}

	// Kiểm tra ngày chạy
	today := nowFull[:10]
	oldDate := ""
	for _, l := range lines {
		if len(l) >= 10 && strings.Contains(l, "/") {
			oldDate = l[:10]
			break
		}
	}

	// Logic tăng/giữ count
	if oldDate != today {
		count = 1 // Qua ngày mới -> Reset về 1
	} else {
		if mode == "reset" {
			count++ // Chạy lại -> Tăng 1
		} else if count == 0 {
			count = 1
		}
		// Nếu mode == "normal" (cleanup) -> Giữ nguyên count
	}

	// Xác định dòng trạng thái đầu tiên
	st := newStatus
	if st == "" && len(lines) > 0 {
		st = lines[0]
	}
	if st == "" {
		st = "Đang chạy"
	}

	return fmt.Sprintf("%s\n%s (Lần %d)", st, nowFull, count)
}
