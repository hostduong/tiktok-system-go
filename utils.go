package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// --- 1. CÁC HÀM TIỆN ÍCH CƠ BẢN ---

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

// 🔥 Helper mới: Chuyển Input (String hoặc Array) thành Slice String chuẩn
func ToSlice(v interface{}) []string {
	if v == nil { return []string{} }
	
	// Nếu là mảng []interface{} (do JSON decode)
	if arr, ok := v.([]interface{}); ok {
		res := make([]string, len(arr))
		for i, item := range arr {
			res[i] = CleanString(item)
		}
		return res
	}
	
	// Nếu là string đơn lẻ
	s := CleanString(v)
	if s != "" { return []string{s} }
	
	// Trường hợp đặc biệt: Input là "" nhưng user muốn filter rỗng -> Vẫn trả về mảng chứa ""
	if s == "" {
		// Kiểm tra xem v có thực sự là chuỗi rỗng không hay là nil
		if strVal, ok := v.(string); ok && strVal == "" {
			return []string{""}
		}
	}
	
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

// --- 2. HÀM KIỂM TRA CHẤT LƯỢNG NICK ---

type QualityResult struct {
	Valid       bool
	SystemEmail string
	Missing     string
}

func GetFloatVal(row []interface{}, idx int) (float64, bool) {
	if idx >= len(row) { return 0, false }
	return toFloat(row[idx])
}

func KiemTraChatLuongClean(cleanRow []string, action string) QualityResult {
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

// --- 3. STRUCT & MAPPING 61 TRƯỜNG ---

// AuthProfile: 0-22
type AuthProfile struct {
	Status        string `json:"status"`
	Note          string `json:"note"`
	DeviceId      string `json:"device_id"`
	UserId        string `json:"user_id"`
	UserSec       string `json:"user_sec"`
	UserName      string `json:"user_name"`
	Email         string `json:"email"`
	NickName      string `json:"nick_name"`
	Password      string `json:"password"`
	PasswordEmail string `json:"password_email"`
	RecoveryEmail string `json:"recovery_email"`
	TwoFa         string `json:"two_fa"`
	Phone         string `json:"phone"`
	Birthday      string `json:"birthday"`
	ClientId      string `json:"client_id"`
	RefreshToken  string `json:"refresh_token"`
	AccessToken   string `json:"access_token"`
	Cookie        string `json:"cookie"`
	UserAgent     string `json:"user_agent"`
	Proxy         string `json:"proxy"`
	ProxyExpired  string `json:"proxy_expired"`
	CreateCountry string `json:"create_country"`
	CreateTime    string `json:"create_time"`
}

// ActivityProfile: 23-44
type ActivityProfile struct {
	StatusPost       string `json:"status_post"`
	DailyPostLimit   string `json:"daily_post_limit"`
	TodayPostCount   string `json:"today_post_count"`
	DailyFollowLimit string `json:"daily_follow_limit"`
	TodayFollowCount string `json:"today_follow_count"`
	LastActiveDate   string `json:"last_active_date"`
	FollowerCount    string `json:"follower_count"`
	FollowingCount   string `json:"following_count"`
	LikesCount       string `json:"likes_count"`
	VideoCount       string `json:"video_count"`
	StatusLive       string `json:"status_live"`
	LivePhoneAccess  string `json:"live_phone_access"`
	LiveStudioAccess string `json:"live_studio_access"`
	LiveKey          string `json:"live_key"`
	LastLiveDuration string `json:"last_live_duration"`
	ShopRole         string `json:"shop_role"`
	ShopId           string `json:"shop_id"`
	ProductCount     string `json:"product_count"`
	ShopHealth       string `json:"shop_health"`
	TotalOrders      string `json:"total_orders"`
	TotalRevenue     string `json:"total_revenue"`
	CommissionRate   string `json:"commission_rate"`
}

// AiProfile: 45-60
type AiProfile struct {
	Signature         string `json:"signature"`
	DefaultCategory   string `json:"default_category"`
	DefaultProduct    string `json:"default_product"`
	PreferredKeywords string `json:"preferred_keywords"`
	PreferredHashtags string `json:"preferred_hashtags"`
	WritingStyle      string `json:"writing_style"`
	MainGoal          string `json:"main_goal"`
	DefaultCta        string `json:"default_cta"`
	ContentLength     string `json:"content_length"`
	ContentType       string `json:"content_type"`
	TargetAudience    string `json:"target_audience"`
	VisualStyle       string `json:"visual_style"`
	AiPersona         string `json:"ai_persona"`
	BannedKeywords    string `json:"banned_keywords"`
	ContentLanguage   string `json:"content_language"`
	Country           string `json:"country"`
}

func gs(row []interface{}, idx int) string {
	if idx >= 0 && idx < len(row) { return fmt.Sprintf("%v", row[idx]) }
	return ""
}

func MakeAuthProfile(row []interface{}) AuthProfile {
	return AuthProfile{
		Status: gs(row, 0), Note: gs(row, 1), DeviceId: gs(row, 2), UserId: gs(row, 3), UserSec: gs(row, 4),
		UserName: gs(row, 5), Email: gs(row, 6), NickName: gs(row, 7), Password: gs(row, 8), PasswordEmail: gs(row, 9),
		RecoveryEmail: gs(row, 10), TwoFa: gs(row, 11), Phone: gs(row, 12), Birthday: gs(row, 13), ClientId: gs(row, 14),
		RefreshToken: gs(row, 15), AccessToken: gs(row, 16), Cookie: gs(row, 17), UserAgent: gs(row, 18), Proxy: gs(row, 19),
		ProxyExpired: gs(row, 20), CreateCountry: gs(row, 21), CreateTime: gs(row, 22),
	}
}

func MakeActivityProfile(row []interface{}) ActivityProfile {
	return ActivityProfile{
		StatusPost: gs(row, 23), DailyPostLimit: gs(row, 24), TodayPostCount: gs(row, 25), DailyFollowLimit: gs(row, 26),
		TodayFollowCount: gs(row, 27), LastActiveDate: gs(row, 28), FollowerCount: gs(row, 29), FollowingCount: gs(row, 30),
		LikesCount: gs(row, 31), VideoCount: gs(row, 32), StatusLive: gs(row, 33), LivePhoneAccess: gs(row, 34),
		LiveStudioAccess: gs(row, 35), LiveKey: gs(row, 36), LastLiveDuration: gs(row, 37), ShopRole: gs(row, 38),
		ShopId: gs(row, 39), ProductCount: gs(row, 40), ShopHealth: gs(row, 41), TotalOrders: gs(row, 42),
		TotalRevenue: gs(row, 43), CommissionRate: gs(row, 44),
	}
}

func MakeAiProfile(row []interface{}) AiProfile {
	return AiProfile{
		Signature: gs(row, 45), DefaultCategory: gs(row, 46), DefaultProduct: gs(row, 47), PreferredKeywords: gs(row, 48),
		PreferredHashtags: gs(row, 49), WritingStyle: gs(row, 50), MainGoal: gs(row, 51), DefaultCta: gs(row, 52),
		ContentLength: gs(row, 53), ContentType: gs(row, 54), TargetAudience: gs(row, 55), VisualStyle: gs(row, 56),
		AiPersona: gs(row, 57), BannedKeywords: gs(row, 58), ContentLanguage: gs(row, 59), Country: gs(row, 60),
	}
}

// Mapping dùng cho update
func getKeyName(idx int) string {
	// ... (Giữ nguyên như cũ nếu cần, hoặc dùng Struct trên kia)
	// Để đơn giản, handler_update sẽ dùng các hàm Make... trả về Struct,
	// sau đó encode JSON thì key sẽ tự đúng theo tag `json:"..."`.
	return "" 
}
func AnhXaAuth(row []interface{}) map[string]interface{} { return nil } // Placeholder để tương thích code cũ nếu có
