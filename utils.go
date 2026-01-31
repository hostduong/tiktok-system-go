package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// =================================================================================================
// 🟢 1. CÁC HÀM TIỆN ÍCH CƠ BẢN (HELPER FUNCTIONS)
// =================================================================================================

// CleanString: Chuẩn hóa dữ liệu về dạng chuỗi viết thường, cắt khoảng trắng.
// Đặc biệt xử lý số lớn (ID) để không bị lỗi e+18 (khoa học).
func CleanString(v interface{}) string {
	if v == nil { return "" } // Nếu nil trả về rỗng
	// Nếu là số float64 (Google Sheet trả về), ép kiểu giữ nguyên độ chính xác (-1)
	if f, ok := v.(float64); ok { return strings.TrimSpace(strconv.FormatFloat(f, 'f', -1, 64)) }
	// Các kiểu khác ép về string, cắt khoảng trắng và viết thường
	return strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
}

// SafeString: Giống CleanString nhưng GIỮ NGUYÊN HOA THƯỜNG (Dùng cho Note, Password...)
func SafeString(v interface{}) string {
	if v == nil { return "" }
	if f, ok := v.(float64); ok { return strings.TrimSpace(strconv.FormatFloat(f, 'f', -1, 64)) }
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// toFloat: Cố gắng chuyển mọi kiểu dữ liệu về float64 để so sánh số học
func toFloat(v interface{}) (float64, bool) {
	if f, ok := v.(float64); ok { return f, true } // Đã là số thì trả về luôn
	if s, ok := v.(string); ok {
		// Nếu là chuỗi thì parse sang số
		if f, err := strconv.ParseFloat(s, 64); err == nil { return f, true }
	}
	return 0, false // Không chuyển được
}

// getFloatVal: Lấy giá trị số tại cột cụ thể trong dòng
func getFloatVal(row []interface{}, idx int) (float64, bool) {
	if idx < 0 || idx >= len(row) { return 0, false } // Check index bound
	return toFloat(row[idx])
}

// ToSlice: Chuyển input thành mảng String. Hỗ trợ cả String đơn và Array.
// Ví dụ: "abc" -> ["abc"], ["a", "b"] -> ["a", "b"]
func ToSlice(v interface{}) []string {
	if v == nil { return []string{} }
	// Nếu input là mảng
	if arr, ok := v.([]interface{}); ok {
		res := make([]string, len(arr))
		for i, item := range arr { res[i] = CleanString(item) } // Clean từng phần tử
		return res
	}
	// Nếu input là chuỗi đơn
	s := CleanString(v)
	if s != "" { return []string{s} }
	return []string{}
}

// ConvertSerialDate: Chuyển đổi ngày tháng (Excel Serial hoặc String) sang Unix Millis
func ConvertSerialDate(v interface{}) int64 {
	s := fmt.Sprintf("%v", v)
	// Trường hợp 1: Dạng chuỗi dd/mm/yyyy
	if strings.Contains(s, "/") {
		if t, err := time.ParseInLocation("02/01/2006 15:04:05", s, time.FixedZone("UTC+7", 7*3600)); err == nil { return t.UnixMilli() }
		if t, err := time.ParseInLocation("02/01/2006", s, time.FixedZone("UTC+7", 7*3600)); err == nil { return t.UnixMilli() }
	}
	// Trường hợp 2: Dạng số Serial của Excel (tính từ 30/12/1899)
	val := 0.0
	if f, ok := v.(float64); ok { val = f } else if f, err := strconv.ParseFloat(s, 64); err == nil { val = f }
	if val > 0 {
		t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		days := int(math.Floor(val))
		seconds := int((val - float64(days)) * 86400)
		return t.AddDate(0, 0, days).Add(time.Duration(seconds) * time.Second).UnixMilli()
	}
	return 0
}

// =================================================================================================
// 🔥 2. BỘ MÁY LỌC MỚI (ROOT LEVEL SEARCH ENGINE)
// =================================================================================================

// CriteriaSet: Chứa tập hợp các điều kiện tìm kiếm (Match, Contains, Min, Max...)
type CriteriaSet struct {
	MatchCols    map[int][]string  // Cột X phải KHỚP chính xác 1 trong các giá trị
	ContainsCols map[int][]string  // Cột X phải CHỨA 1 trong các giá trị
	MinCols      map[int]float64   // Cột X >= Giá trị
	MaxCols      map[int]float64   // Cột X <= Giá trị
	TimeCols     map[int]float64   // Cột X trong vòng Y giờ gần nhất
	IsEmpty      bool              // Đánh dấu xem set này có điều kiện nào không
}

// FilterParams: Chứa 2 nhóm điều kiện AND và OR
type FilterParams struct {
	AndCriteria CriteriaSet // Tất cả phải đúng
	OrCriteria  CriteriaSet // Ít nhất 1 cái đúng
	HasFilter   bool        // Có filter hay không
}

// parseCriteriaSet: Hàm parse 1 block JSON (ví dụ search_and) thành struct CriteriaSet
func parseCriteriaSet(input interface{}) CriteriaSet {
	c := CriteriaSet{
		MatchCols: make(map[int][]string), ContainsCols: make(map[int][]string),
		MinCols: make(map[int]float64), MaxCols: make(map[int]float64), TimeCols: make(map[int]float64),
		IsEmpty: true,
	}
	data, ok := input.(map[string]interface{})
	if !ok { return c }

	// Quét qua từng key trong JSON
	for k, v := range data {
		if strings.HasPrefix(k, "match_col_") {
			// Parse index từ tên key (ví dụ match_col_5 -> index 5)
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
		}
	}
	return c
}

// parseFilterParams: Đọc filter từ Root Body (search_and, search_or)
func parseFilterParams(body map[string]interface{}) FilterParams {
	f := FilterParams{HasFilter: false}

	// 1. Tìm và parse search_and
	if v, ok := body["search_and"]; ok {
		f.AndCriteria = parseCriteriaSet(v)
	}

	// 2. Tìm và parse search_or
	if v, ok := body["search_or"]; ok {
		f.OrCriteria = parseCriteriaSet(v)
	}

	// Nếu có bất kỳ điều kiện nào -> Bật cờ lọc để Code xử lý logic filter
	if !f.AndCriteria.IsEmpty || !f.OrCriteria.IsEmpty {
		f.HasFilter = true
	}
	return f
}

// checkCriteriaMatch: Kiểm tra 1 dòng có khớp với 1 tập CriteriaSet không
// modeMatchAll: True (Logic AND - Phải khớp hết), False (Logic OR - Khớp 1 cái là được)
func checkCriteriaMatch(cleanRow []string, rawRow []interface{}, c CriteriaSet, modeMatchAll bool) bool {
	if c.IsEmpty { return true } // Không có điều kiện -> Luôn đúng
	
	// Helper xử lý kết quả nhanh
	processResult := func(isMatch bool) (bool, bool) {
		if modeMatchAll { if !isMatch { return false, true } } else { if isMatch { return true, true } } // AND: Sai 1 cái là dừng. OR: Đúng 1 cái là dừng.
		return false, false
	}

	// Check Match Cols
	for idx, targets := range c.MatchCols {
		cellVal := ""; if idx < len(cleanRow) { cellVal = cleanRow[idx] }
		match := false
		for _, t := range targets { if t == cellVal { match = true; break } } // So sánh bằng
		if res, stop := processResult(match); stop { return res }
	}

	// Check Contains Cols
	for idx, targets := range c.ContainsCols {
		cellVal := ""; if idx < len(cleanRow) { cellVal = cleanRow[idx] }
		match := false
		for _, t := range targets {
			if t == "" { if cellVal == "" { match = true; break } } else { if strings.Contains(cellVal, t) { match = true; break } } // So sánh chứa
		}
		if res, stop := processResult(match); stop { return res }
	}

	// Check Min/Max
	for idx, minVal := range c.MinCols {
		val, ok := getFloatVal(rawRow, idx); match := ok && val >= minVal
		if res, stop := processResult(match); stop { return res }
	}
	for idx, maxVal := range c.MaxCols {
		val, ok := getFloatVal(rawRow, idx); match := ok && val <= maxVal
		if res, stop := processResult(match); stop { return res }
	}
	
	// Check Time
	now := time.Now().UnixMilli()
	for idx, hours := range c.TimeCols {
		timeVal := int64(0); if idx < len(rawRow) { timeVal = ConvertSerialDate(rawRow[idx]) }
		match := timeVal > 0 && (float64(now-timeVal)/3600000.0 <= hours) // Đổi ra giờ
		if res, stop := processResult(match); stop { return res }
	}

	// Nếu chạy hết vòng lặp: AND -> True, OR -> False (vì chưa hit cái nào)
	if modeMatchAll { return true } else { return false }
}

// isRowMatched: Hàm chính kiểm tra dòng dữ liệu
func isRowMatched(cleanRow []string, rawRow []interface{}, f FilterParams) bool {
	// Logic: (Thỏa mãn nhóm AND) VÀ (Thỏa mãn nhóm OR)
	if !f.AndCriteria.IsEmpty {
		if !checkCriteriaMatch(cleanRow, rawRow, f.AndCriteria, true) { return false }
	}
	if !f.OrCriteria.IsEmpty {
		if !checkCriteriaMatch(cleanRow, rawRow, f.OrCriteria, false) { return false }
	}
	return true
}

// =================================================================================================
// 🟢 3. KIỂM TRA CHẤT LƯỢNG NICK (QUALITY CHECK)
// =================================================================================================

type QualityResult struct { Valid bool; SystemEmail string; Missing string }

func KiemTraChatLuongClean(cleanRow []string, action string) QualityResult {
	// Check độ dài dữ liệu
	if len(cleanRow) <= INDEX_DATA_TIKTOK.EMAIL { return QualityResult{false, "", "data_length"} }
	
	// Parse System Email từ Email gốc
	rawEmail := cleanRow[INDEX_DATA_TIKTOK.EMAIL]
	sysEmail := ""
	if strings.Contains(rawEmail, "@") { parts := strings.Split(rawEmail, "@"); if len(parts) > 1 { sysEmail = parts[1] } }
	
	if action == "view_only" { return QualityResult{true, sysEmail, ""} }

	hasEmail := (rawEmail != "")
	hasUser := (cleanRow[INDEX_DATA_TIKTOK.USER_NAME] != "")
	hasPass := (cleanRow[INDEX_DATA_TIKTOK.PASSWORD] != "")

	// Logic check từng action
	if strings.Contains(action, "register") {
		// Register cần Email
		if hasEmail { return QualityResult{true, sysEmail, ""} }
		return QualityResult{false, "", "email"}
	}
	if strings.Contains(action, "login") || strings.Contains(action, "auto") {
		// Login/Auto cần (User hoặc Email) VÀ Pass
		if (hasEmail || hasUser) && hasPass { return QualityResult{true, sysEmail, ""} }
		return QualityResult{false, "", "user/pass"}
	}
	return QualityResult{false, "", "unknown"}
}

// =================================================================================================
// 🟢 4. PROFILE STRUCTS (Cấu trúc trả về JSON)
// =================================================================================================

type AuthProfile struct { Status string `json:"status"`; Note string `json:"note"`; DeviceId string `json:"device_id"`; UserId string `json:"user_id"`; UserSec string `json:"user_sec"`; UserName string `json:"user_name"`; Email string `json:"email"`; NickName string `json:"nick_name"`; Password string `json:"password"`; PasswordEmail string `json:"password_email"`; RecoveryEmail string `json:"recovery_email"`; TwoFa string `json:"two_fa"`; Phone string `json:"phone"`; Birthday string `json:"birthday"`; ClientId string `json:"client_id"`; RefreshToken string `json:"refresh_token"`; AccessToken string `json:"access_token"`; Cookie string `json:"cookie"`; UserAgent string `json:"user_agent"`; Proxy string `json:"proxy"`; ProxyExpired string `json:"proxy_expired"`; CreateCountry string `json:"create_country"`; CreateTime string `json:"create_time"` }
type ActivityProfile struct { StatusPost string `json:"status_post"`; DailyPostLimit string `json:"daily_post_limit"`; TodayPostCount string `json:"today_post_count"`; DailyFollowLimit string `json:"daily_follow_limit"`; TodayFollowCount string `json:"today_follow_count"`; LastActiveDate string `json:"last_active_date"`; FollowerCount string `json:"follower_count"`; FollowingCount string `json:"following_count"`; LikesCount string `json:"likes_count"`; VideoCount string `json:"video_count"`; StatusLive string `json:"status_live"`; LivePhoneAccess string `json:"live_phone_access"`; LiveStudioAccess string `json:"live_studio_access"`; LiveKey string `json:"live_key"`; LastLiveDuration string `json:"last_live_duration"`; ShopRole string `json:"shop_role"`; ShopId string `json:"shop_id"`; ProductCount string `json:"product_count"`; ShopHealth string `json:"shop_health"`; TotalOrders string `json:"total_orders"`; TotalRevenue string `json:"total_revenue"`; CommissionRate string `json:"commission_rate"` }
type AiProfile struct { Signature string `json:"signature"`; DefaultCategory string `json:"default_category"`; DefaultProduct string `json:"default_product"`; PreferredKeywords string `json:"preferred_keywords"`; PreferredHashtags string `json:"preferred_hashtags"`; WritingStyle string `json:"writing_style"`; MainGoal string `json:"main_goal"`; DefaultCta string `json:"default_cta"`; ContentLength string `json:"content_length"`; ContentType string `json:"content_type"`; TargetAudience string `json:"target_audience"`; VisualStyle string `json:"visual_style"`; AiPersona string `json:"ai_persona"`; BannedKeywords string `json:"banned_keywords"`; ContentLanguage string `json:"content_language"`; Country string `json:"country"` }

func gs(row []interface{}, idx int) string { if idx >= 0 && idx < len(row) { return fmt.Sprintf("%v", row[idx]) }; return "" }

// Các hàm Mapper từ Row -> Struct
func MakeAuthProfile(row []interface{}) AuthProfile { return AuthProfile{ Status: gs(row, 0), Note: gs(row, 1), DeviceId: gs(row, 2), UserId: gs(row, 3), UserSec: gs(row, 4), UserName: gs(row, 5), Email: gs(row, 6), NickName: gs(row, 7), Password: gs(row, 8), PasswordEmail: gs(row, 9), RecoveryEmail: gs(row, 10), TwoFa: gs(row, 11), Phone: gs(row, 12), Birthday: gs(row, 13), ClientId: gs(row, 14), RefreshToken: gs(row, 15), AccessToken: gs(row, 16), Cookie: gs(row, 17), UserAgent: gs(row, 18), Proxy: gs(row, 19), ProxyExpired: gs(row, 20), CreateCountry: gs(row, 21), CreateTime: gs(row, 22) } }
func MakeActivityProfile(row []interface{}) ActivityProfile { return ActivityProfile{ StatusPost: gs(row, 23), DailyPostLimit: gs(row, 24), TodayPostCount: gs(row, 25), DailyFollowLimit: gs(row, 26), TodayFollowCount: gs(row, 27), LastActiveDate: gs(row, 28), FollowerCount: gs(row, 29), FollowingCount: gs(row, 30), LikesCount: gs(row, 31), VideoCount: gs(row, 32), StatusLive: gs(row, 33), LivePhoneAccess: gs(row, 34), LiveStudioAccess: gs(row, 35), LiveKey: gs(row, 36), LastLiveDuration: gs(row, 37), ShopRole: gs(row, 38), ShopId: gs(row, 39), ProductCount: gs(row, 40), ShopHealth: gs(row, 41), TotalOrders: gs(row, 42), TotalRevenue: gs(row, 43), CommissionRate: gs(row, 44) } }
func MakeAiProfile(row []interface{}) AiProfile { return AiProfile{ Signature: gs(row, 45), DefaultCategory: gs(row, 46), DefaultProduct: gs(row, 47), PreferredKeywords: gs(row, 48), PreferredHashtags: gs(row, 49), WritingStyle: gs(row, 50), MainGoal: gs(row, 51), DefaultCta: gs(row, 52), ContentLength: gs(row, 53), ContentType: gs(row, 54), TargetAudience: gs(row, 55), VisualStyle: gs(row, 56), AiPersona: gs(row, 57), BannedKeywords: gs(row, 58), ContentLanguage: gs(row, 59), Country: gs(row, 60) } }

// Helper xóa phần tử khỏi Status Map
func removeFromStatusMap(m map[string][]int, status string, targetIdx int) { if list, ok := m[status]; ok { for i, v := range list { if v == targetIdx { m[status] = append(list[:i], list[i+1:]...); return } } } }
