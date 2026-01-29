package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func CleanString(v interface{}) string {
	if v == nil { return "" }
	return strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
}

func SafeString(v interface{}) string {
	if v == nil { return "" }
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func toFloat(v interface{}) (float64, bool) {
	if f, ok := v.(float64); ok { return f, true }
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(s, 64); err == nil { return f, true }
	}
	return 0, false
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

// Helper lấy tên cột từ Index
func getKeyName(idx int) string {
	switch idx {
	case 0: return "status"; case 1: return "note"; case 2: return "device_id"; case 3: return "user_id"; case 4: return "user_sec"; case 5: return "user_name"; case 6: return "email";
	case 7: return "nick_name"; case 8: return "password"; case 9: return "password_email"; case 10: return "recovery_email"; case 11: return "two_fa";
	case 12: return "phone"; case 13: return "birthday"; case 14: return "client_id"; case 15: return "refresh_token"; case 16: return "access_token";
	case 17: return "cookie"; case 18: return "user_agent"; case 19: return "proxy"; case 20: return "proxy_expired"; case 21: return "create_country"; case 22: return "create_time";
	case 23: return "status_post"; case 24: return "daily_post_limit"; case 25: return "today_post_count"; case 26: return "daily_follow_limit"; case 27: return "today_follow_count"; case 28: return "last_active_date";
	case 29: return "follower_count"; case 30: return "following_count"; case 31: return "likes_count"; case 32: return "video_count"; case 33: return "status_live";
	case 34: return "live_phone_access"; case 35: return "live_studio_access"; case 36: return "live_key"; case 37: return "last_live_duration";
	case 38: return "shop_role"; case 39: return "shop_id"; case 40: return "product_count"; case 41: return "shop_health"; case 42: return "total_orders"; case 43: return "total_revenue"; case 44: return "commission_rate";
	case 45: return "signature"; case 46: return "default_category"; case 47: return "default_product"; case 48: return "preferred_keywords"; case 49: return "preferred_hashtags";
	case 50: return "writing_style"; case 51: return "main_goal"; case 52: return "default_cta"; case 53: return "content_length"; case 54: return "content_type";
	case 55: return "target_audience"; case 56: return "visual_style"; case 57: return "ai_persona"; case 58: return "banned_keywords"; case 59: return "content_language"; case 60: return "country";
	}
	return ""
}

func getString(row []interface{}, idx int) string {
	if idx >= 0 && idx < len(row) { return fmt.Sprintf("%v", row[idx]) }
	return ""
}

// Mapping nhóm AUTH (0 -> 22)
func AnhXaAuth(row []interface{}) map[string]interface{} {
	res := make(map[string]interface{})
	for i := 0; i <= 22; i++ {
		key := getKeyName(i)
		if key != "" { res[key] = getString(row, i) }
	}
	return res
}

// Mapping nhóm ACTIVITY (23 -> 44)
func AnhXaActivity(row []interface{}) map[string]interface{} {
	res := make(map[string]interface{})
	for i := 23; i <= 44; i++ {
		key := getKeyName(i)
		if key != "" { res[key] = getString(row, i) }
	}
	return res
}

// Mapping nhóm AI (45 -> 60)
func AnhXaAi(row []interface{}) map[string]interface{} {
	res := make(map[string]interface{})
	for i := 45; i <= 60; i++ {
		key := getKeyName(i)
		if key != "" { res[key] = getString(row, i) }
	}
	return res
}

// Type QualityResult để dùng chung
type QualityResult struct { Valid bool; SystemEmail string; Missing string }

// Hàm kiểm tra chất lượng nick (dùng chung)
func kiem_tra_chat_luong_clean(cleanRow []string, action string) QualityResult {
	if len(cleanRow) <= INDEX_DATA_TIKTOK.EMAIL { return QualityResult{false, "", "data_length"} }
	rawEmail := cleanRow[INDEX_DATA_TIKTOK.EMAIL]
	sysEmail := ""
	if strings.Contains(rawEmail, "@") { parts := strings.Split(rawEmail, "@"); if len(parts) > 1 { sysEmail = parts[1] } }
	if action == "view_only" { return QualityResult{true, sysEmail, ""} }
	
	hasEmail := (rawEmail != "")
	hasUser := (cleanRow[INDEX_DATA_TIKTOK.USER_NAME] != "")
	hasPass := (cleanRow[INDEX_DATA_TIKTOK.PASSWORD] != "")

	if strings.Contains(action, "register") { if hasEmail { return QualityResult{true, sysEmail, ""} }; return QualityResult{false, "", "email"} }
	if strings.Contains(action, "login") { if (hasEmail || hasUser) && hasPass { return QualityResult{true, sysEmail, ""} }; return QualityResult{false, "", "user/pass"} }
	if action == "auto" { if hasEmail || ((hasUser || hasEmail) && hasPass) { return QualityResult{true, sysEmail, ""} }; return QualityResult{false, "", "data"} }
	return QualityResult{false, "", "unknown"}
}
