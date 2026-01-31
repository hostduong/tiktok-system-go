package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

// =================================================================================================
// 🟢 1. CÁC HÀM XỬ LÝ CHUỖI & DỮ LIỆU CƠ BẢN
// =================================================================================================

// Regex xóa ký tự ẩn (Non-breaking space, Zero-width space...)
var REGEX_INVISIBLE = regexp.MustCompile(`[\x{00A0}\x{200B}\x{200C}\x{200D}\x{FEFF}]`)

// 🔥 ĐÃ CHUYỂN REGEX VỀ ĐÂY CHO ĐÚNG LOGIC ĐÓNG GÓI
var (
	REGEX_DATE  = regexp.MustCompile(`(\d{1,2}\/\d{1,2}\/\d{4})`)
	REGEX_COUNT = regexp.MustCompile(`\(Lần\s*(\d+)\)`)
)

func CleanString(v interface{}) string {
	if v == nil { return "" }
	if f, ok := v.(float64); ok { return strings.TrimSpace(strconv.FormatFloat(f, 'f', -1, 64)) }
	
	s := strings.ToLower(fmt.Sprintf("%v", v))
	s = norm.NFC.String(s)
	s = strings.ReplaceAll(s, "\u00A0", " ")
	s = REGEX_INVISIBLE.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func SafeString(v interface{}) string {
	if v == nil { return "" }
	if f, ok := v.(float64); ok { return strings.TrimSpace(strconv.FormatFloat(f, 'f', -1, 64)) }
	
	s := fmt.Sprintf("%v", v)
	s = norm.NFC.String(s)
	s = strings.ReplaceAll(s, "\u00A0", " ")
	s = REGEX_INVISIBLE.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// ... (Các hàm toFloat, getFloatVal, ToSlice, ConvertSerialDate giữ nguyên) ...
func toFloat(v interface{}) (float64, bool) {
	if f, ok := v.(float64); ok { return f, true }
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(s, 64); err == nil { return f, true }
	}
	return 0, false
}

func getFloatVal(row []interface{}, idx int) (float64, bool) {
	if idx < 0 || idx >= len(row) { return 0, false }
	return toFloat(row[idx])
}

func ToSlice(v interface{}) []string {
	if v == nil { return []string{} }
	if arr, ok := v.([]interface{}); ok {
		res := make([]string, len(arr))
		for i, item := range arr { res[i] = CleanString(item) }
		return res
	}
	s := CleanString(v)
	if s != "" { return []string{s} }
	return []string{}
}

func ConvertSerialDate(v interface{}) int64 {
	s := fmt.Sprintf("%v", v)
	if strings.Contains(s, "/") {
		if t, err := time.ParseInLocation("02/01/2006 15:04:05", s, time.FixedZone("UTC+7", 7*3600)); err == nil { return t.UnixMilli() }
		if t, err := time.ParseInLocation("02/01/2006", s, time.FixedZone("UTC+7", 7*3600)); err == nil { return t.UnixMilli() }
	}
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
// 🟢 2. CÁC HÀM QUẢN LÝ MAP/LIST DÙNG CHUNG
// =================================================================================================

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

func removeFromIntList(list *[]int, target int) {
	for i, v := range *list {
		if v == target {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return
		}
	}
}

// =================================================================================================
// 🟢 3. BỘ MÁY LỌC (FILTER ENGINE)
// =================================================================================================

type CriteriaSet struct {
	MatchCols    map[int][]string
	ContainsCols map[int][]string
	MinCols      map[int]float64
	MaxCols      map[int]float64
	TimeCols     map[int]float64
	IsEmpty      bool
}

type FilterParams struct {
	AndCriteria CriteriaSet
	OrCriteria  CriteriaSet
	HasFilter   bool
}

func NewCriteriaSet() CriteriaSet {
	return CriteriaSet{
		MatchCols:    make(map[int][]string),
		ContainsCols: make(map[int][]string),
		MinCols:      make(map[int]float64),
		MaxCols:      make(map[int]float64),
		TimeCols:     make(map[int]float64),
		IsEmpty:      true,
	}
}

func parseCriteriaSet(input interface{}) CriteriaSet {
	c := NewCriteriaSet()
	data, ok := input.(map[string]interface{})
	if !ok { return c }

	for k, v := range data {
		if strings.HasPrefix(k, "match_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "match_col_")); err == nil {
				vals := ToSlice(v)
				if len(vals) > 0 && vals[0] != "" { c.MatchCols[idx] = vals; c.IsEmpty = false }
			}
		} else if strings.HasPrefix(k, "contains_col_") {
			if idx, err := strconv.Atoi(strings.TrimPrefix(k, "contains_col_")); err == nil {
				vals := ToSlice(v)
				if len(vals) > 0 && vals[0] != "" { c.ContainsCols[idx] = vals; c.IsEmpty = false }
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

func parseFilterParams(body map[string]interface{}) FilterParams {
	f := FilterParams{
		AndCriteria: NewCriteriaSet(),
		OrCriteria:  NewCriteriaSet(),
		HasFilter:   false,
	}
	if v, ok := body["search_and"]; ok { f.AndCriteria = parseCriteriaSet(v) }
	if v, ok := body["search_or"]; ok { f.OrCriteria = parseCriteriaSet(v) }
	
	if !f.AndCriteria.IsEmpty || !f.OrCriteria.IsEmpty { f.HasFilter = true }
	return f
}

func checkCriteriaMatch(cleanRow []string, rawRow []interface{}, c CriteriaSet, modeMatchAll bool) bool {
	if c.IsEmpty { return true }
	
	processResult := func(isMatch bool) (bool, bool) {
		if modeMatchAll { if !isMatch { return false, true } } else { if isMatch { return true, true } }
		return false, false
	}

	for idx, targets := range c.MatchCols {
		cellVal := ""; if idx < len(cleanRow) { cellVal = cleanRow[idx] }
		match := false
		for _, t := range targets { if t == cellVal { match = true; break } }
		if res, stop := processResult(match); stop { return res }
	}
	for idx, targets := range c.ContainsCols {
		cellVal := ""; if idx < len(cleanRow) { cellVal = cleanRow[idx] }
		match := false
		for _, t := range targets {
			if t == "" { if cellVal == "" { match = true; break } } else { if strings.Contains(cellVal, t) { match = true; break } }
		}
		if res, stop := processResult(match); stop { return res }
	}
	for idx, minVal := range c.MinCols {
		val, ok := getFloatVal(rawRow, idx); match := ok && val >= minVal
		if res, stop := processResult(match); stop { return res }
	}
	for idx, maxVal := range c.MaxCols {
		val, ok := getFloatVal(rawRow, idx); match := ok && val <= maxVal
		if res, stop := processResult(match); stop { return res }
	}
	now := time.Now().UnixMilli()
	for idx, hours := range c.TimeCols {
		timeVal := int64(0); if idx < len(rawRow) { timeVal = ConvertSerialDate(rawRow[idx]) }
		match := timeVal > 0 && (float64(now-timeVal)/3600000.0 <= hours)
		if res, stop := processResult(match); stop { return res }
	}

	if modeMatchAll { return true } else { return false }
}

func isRowMatched(cleanRow []string, rawRow []interface{}, f FilterParams) bool {
	if !f.AndCriteria.IsEmpty {
		if !checkCriteriaMatch(cleanRow, rawRow, f.AndCriteria, true) { return false }
	}
	if !f.OrCriteria.IsEmpty {
		if !checkCriteriaMatch(cleanRow, rawRow, f.OrCriteria, false) { return false }
	}
	return true
}

// =================================================================================================
// 🟢 4. KIỂM TRA CHẤT LƯỢNG & TẠO PROFILE
// =================================================================================================

type QualityResult struct { Valid bool; SystemEmail string; Missing string }

func KiemTraChatLuongClean(cleanRow []string, action string) QualityResult {
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
	if strings.Contains(action, "login") || strings.Contains(action, "auto") {
		if (hasEmail || hasUser) && hasPass { return QualityResult{true, sysEmail, ""} }
		return QualityResult{false, "", "user/pass"}
	}
	return QualityResult{false, "", "unknown"}
}

type AuthProfile struct { Status string `json:"status"`; Note string `json:"note"`; DeviceId string `json:"device_id"`; UserId string `json:"user_id"`; UserSec string `json:"user_sec"`; UserName string `json:"user_name"`; Email string `json:"email"`; NickName string `json:"nick_name"`; Password string `json:"password"`; PasswordEmail string `json:"password_email"`; RecoveryEmail string `json:"recovery_email"`; TwoFa string `json:"two_fa"`; Phone string `json:"phone"`; Birthday string `json:"birthday"`; ClientId string `json:"client_id"`; RefreshToken string `json:"refresh_token"`; AccessToken string `json:"access_token"`; Cookie string `json:"cookie"`; UserAgent string `json:"user_agent"`; Proxy string `json:"proxy"`; ProxyExpired string `json:"proxy_expired"`; CreateCountry string `json:"create_country"`; CreateTime string `json:"create_time"` }
type ActivityProfile struct { StatusPost string `json:"status_post"`; DailyPostLimit string `json:"daily_post_limit"`; TodayPostCount string `json:"today_post_count"`; DailyFollowLimit string `json:"daily_follow_limit"`; TodayFollowCount string `json:"today_follow_count"`; LastActiveDate string `json:"last_active_date"`; FollowerCount string `json:"follower_count"`; FollowingCount string `json:"following_count"`; LikesCount string `json:"likes_count"`; VideoCount string `json:"video_count"`; StatusLive string `json:"status_live"`; LivePhoneAccess string `json:"live_phone_access"`; LiveStudioAccess string `json:"live_studio_access"`; LiveKey string `json:"live_key"`; LastLiveDuration string `json:"last_live_duration"`; ShopRole string `json:"shop_role"`; ShopId string `json:"shop_id"`; ProductCount string `json:"product_count"`; ShopHealth string `json:"shop_health"`; TotalOrders string `json:"total_orders"`; TotalRevenue string `json:"total_revenue"`; CommissionRate string `json:"commission_rate"` }
type AiProfile struct { Signature string `json:"signature"`; DefaultCategory string `json:"default_category"`; DefaultProduct string `json:"default_product"`; PreferredKeywords string `json:"preferred_keywords"`; PreferredHashtags string `json:"preferred_hashtags"`; WritingStyle string `json:"writing_style"`; MainGoal string `json:"main_goal"`; DefaultCta string `json:"default_cta"`; ContentLength string `json:"content_length"`; ContentType string `json:"content_type"`; TargetAudience string `json:"target_audience"`; VisualStyle string `json:"visual_style"`; AiPersona string `json:"ai_persona"`; BannedKeywords string `json:"banned_keywords"`; ContentLanguage string `json:"content_language"`; Country string `json:"country"` }

func gs(row []interface{}, idx int) string { if idx >= 0 && idx < len(row) { return fmt.Sprintf("%v", row[idx]) }; return "" }
func MakeAuthProfile(row []interface{}) AuthProfile { return AuthProfile{ Status: gs(row, 0), Note: gs(row, 1), DeviceId: gs(row, 2), UserId: gs(row, 3), UserSec: gs(row, 4), UserName: gs(row, 5), Email: gs(row, 6), NickName: gs(row, 7), Password: gs(row, 8), PasswordEmail: gs(row, 9), RecoveryEmail: gs(row, 10), TwoFa: gs(row, 11), Phone: gs(row, 12), Birthday: gs(row, 13), ClientId: gs(row, 14), RefreshToken: gs(row, 15), AccessToken: gs(row, 16), Cookie: gs(row, 17), UserAgent: gs(row, 18), Proxy: gs(row, 19), ProxyExpired: gs(row, 20), CreateCountry: gs(row, 21), CreateTime: gs(row, 22) } }
func MakeActivityProfile(row []interface{}) ActivityProfile { return ActivityProfile{ StatusPost: gs(row, 23), DailyPostLimit: gs(row, 24), TodayPostCount: gs(row, 25), DailyFollowLimit: gs(row, 26), TodayFollowCount: gs(row, 27), LastActiveDate: gs(row, 28), FollowerCount: gs(row, 29), FollowingCount: gs(row, 30), LikesCount: gs(row, 31), VideoCount: gs(row, 32), StatusLive: gs(row, 33), LivePhoneAccess: gs(row, 34), LiveStudioAccess: gs(row, 35), LiveKey: gs(row, 36), LastLiveDuration: gs(row, 37), ShopRole: gs(row, 38), ShopId: gs(row, 39), ProductCount: gs(row, 40), ShopHealth: gs(row, 41), TotalOrders: gs(row, 42), TotalRevenue: gs(row, 43), CommissionRate: gs(row, 44) } }
func MakeAiProfile(row []interface{}) AiProfile { return AiProfile{ Signature: gs(row, 45), DefaultCategory: gs(row, 46), DefaultProduct: gs(row, 47), PreferredKeywords: gs(row, 48), PreferredHashtags: gs(row, 49), WritingStyle: gs(row, 50), MainGoal: gs(row, 51), DefaultCta: gs(row, 52), ContentLength: gs(row, 53), ContentType: gs(row, 54), TargetAudience: gs(row, 55), VisualStyle: gs(row, 56), AiPersona: gs(row, 57), BannedKeywords: gs(row, 58), ContentLanguage: gs(row, 59), Country: gs(row, 60) } }
