package main

import (
	"fmt"
	"strings"
)

// =================================================================================================
// 🔥 ĐỊNH NGHĨA KHUÔN MẪU PROFILE (Để ép thứ tự JSON chuẩn Node.js)
// =================================================================================================

type AuthProfile struct {
	Status         string `json:"status"`
	Note           string `json:"note"`
	DeviceId       string `json:"device_id"`
	UserId         string `json:"user_id"`
	UserSec        string `json:"user_sec"`
	UserName       string `json:"user_name"`
	Email          string `json:"email"`
	NickName       string `json:"nick_name"`
	Password       string `json:"password"`
	PasswordEmail  string `json:"password_email"`
	RecoveryEmail  string `json:"recovery_email"`
	TwoFa          string `json:"two_fa"`
	Phone          string `json:"phone"`
	Birthday       string `json:"birthday"`
	ClientId       string `json:"client_id"`
	RefreshToken   string `json:"refresh_token"`
	AccessToken    string `json:"access_token"`
	Cookie         string `json:"cookie"`
	UserAgent      string `json:"user_agent"`
	Proxy          string `json:"proxy"`
	ProxyExpired   string `json:"proxy_expired"`
	CreateCountry  string `json:"create_country"`
	CreateTime     string `json:"create_time"`
}

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

// =================================================================================================
// 🟢 UTILS FUNCTIONS
// =================================================================================================

func CleanString(v interface{}) string {
	if v == nil { return "" }
	str := fmt.Sprintf("%v", v)
	return strings.TrimSpace(strings.ToLower(str))
}

// SafeString: Giữ nguyên định dạng số (không bị e+08)
func SafeString(v interface{}) string {
	if v == nil { return "" }
	switch val := v.(type) {
	case string: return val
	case float64:
		if val == float64(int64(val)) { return fmt.Sprintf("%.0f", val) }
		return fmt.Sprintf("%v", val)
	default: return fmt.Sprintf("%v", val)
	}
}

// Helper lấy giá trị an toàn từ mảng row theo index
func getVal(row []interface{}, idx int) string {
	if idx < len(row) {
		return SafeString(row[idx])
	}
	return ""
}

// 🔥 HÀM MAP DỮ LIỆU VÀO STRUCT (Đảm bảo đúng thứ tự)
func MakeAuthProfile(row []interface{}) AuthProfile {
	return AuthProfile{
		Status:        getVal(row, INDEX_DATA_TIKTOK.STATUS),
		Note:          getVal(row, INDEX_DATA_TIKTOK.NOTE),
		DeviceId:      getVal(row, INDEX_DATA_TIKTOK.DEVICE_ID),
		UserId:        getVal(row, INDEX_DATA_TIKTOK.USER_ID),
		UserSec:       getVal(row, INDEX_DATA_TIKTOK.USER_SEC),
		UserName:      getVal(row, INDEX_DATA_TIKTOK.USER_NAME),
		Email:         getVal(row, INDEX_DATA_TIKTOK.EMAIL),
		NickName:      getVal(row, INDEX_DATA_TIKTOK.NICK_NAME),
		Password:      getVal(row, INDEX_DATA_TIKTOK.PASSWORD),
		PasswordEmail: getVal(row, INDEX_DATA_TIKTOK.PASSWORD_EMAIL),
		RecoveryEmail: getVal(row, INDEX_DATA_TIKTOK.RECOVERY_EMAIL),
		TwoFa:         getVal(row, INDEX_DATA_TIKTOK.TWO_FA),
		Phone:         getVal(row, INDEX_DATA_TIKTOK.PHONE),
		Birthday:      getVal(row, INDEX_DATA_TIKTOK.BIRTHDAY),
		ClientId:      getVal(row, INDEX_DATA_TIKTOK.CLIENT_ID),
		RefreshToken:  getVal(row, INDEX_DATA_TIKTOK.REFRESH_TOKEN),
		AccessToken:   getVal(row, INDEX_DATA_TIKTOK.ACCESS_TOKEN),
		Cookie:        getVal(row, INDEX_DATA_TIKTOK.COOKIE),
		UserAgent:     getVal(row, INDEX_DATA_TIKTOK.USER_AGENT),
		Proxy:         getVal(row, INDEX_DATA_TIKTOK.PROXY),
		ProxyExpired:  getVal(row, INDEX_DATA_TIKTOK.PROXY_EXPIRED),
		CreateCountry: getVal(row, INDEX_DATA_TIKTOK.CREATE_COUNTRY),
		CreateTime:    getVal(row, INDEX_DATA_TIKTOK.CREATE_TIME),
	}
}

func MakeActivityProfile(row []interface{}) ActivityProfile {
	return ActivityProfile{
		StatusPost:       getVal(row, INDEX_DATA_TIKTOK.STATUS_POST),
		DailyPostLimit:   getVal(row, INDEX_DATA_TIKTOK.DAILY_POST_LIMIT),
		TodayPostCount:   getVal(row, INDEX_DATA_TIKTOK.TODAY_POST_COUNT),
		DailyFollowLimit: getVal(row, INDEX_DATA_TIKTOK.DAILY_FOLLOW_LIMIT),
		TodayFollowCount: getVal(row, INDEX_DATA_TIKTOK.TODAY_FOLLOW_COUNT),
		LastActiveDate:   getVal(row, INDEX_DATA_TIKTOK.LAST_ACTIVE_DATE),
		FollowerCount:    getVal(row, INDEX_DATA_TIKTOK.FOLLOWER_COUNT),
		FollowingCount:   getVal(row, INDEX_DATA_TIKTOK.FOLLOWING_COUNT),
		LikesCount:       getVal(row, INDEX_DATA_TIKTOK.LIKES_COUNT),
		VideoCount:       getVal(row, INDEX_DATA_TIKTOK.VIDEO_COUNT),
		StatusLive:       getVal(row, INDEX_DATA_TIKTOK.STATUS_LIVE),
		LivePhoneAccess:  getVal(row, INDEX_DATA_TIKTOK.LIVE_PHONE_ACCESS),
		LiveStudioAccess: getVal(row, INDEX_DATA_TIKTOK.LIVE_STUDIO_ACCESS),
		LiveKey:          getVal(row, INDEX_DATA_TIKTOK.LIVE_KEY),
		LastLiveDuration: getVal(row, INDEX_DATA_TIKTOK.LAST_LIVE_DURATION),
		ShopRole:         getVal(row, INDEX_DATA_TIKTOK.SHOP_ROLE),
		ShopId:           getVal(row, INDEX_DATA_TIKTOK.SHOP_ID),
		ProductCount:     getVal(row, INDEX_DATA_TIKTOK.PRODUCT_COUNT),
		ShopHealth:       getVal(row, INDEX_DATA_TIKTOK.SHOP_HEALTH),
		TotalOrders:      getVal(row, INDEX_DATA_TIKTOK.TOTAL_ORDERS),
		TotalRevenue:     getVal(row, INDEX_DATA_TIKTOK.TOTAL_REVENUE),
		CommissionRate:   getVal(row, INDEX_DATA_TIKTOK.COMMISSION_RATE),
	}
}

func MakeAiProfile(row []interface{}) AiProfile {
	return AiProfile{
		Signature:         getVal(row, INDEX_DATA_TIKTOK.SIGNATURE),
		DefaultCategory:   getVal(row, INDEX_DATA_TIKTOK.DEFAULT_CATEGORY),
		DefaultProduct:    getVal(row, INDEX_DATA_TIKTOK.DEFAULT_PRODUCT),
		PreferredKeywords: getVal(row, INDEX_DATA_TIKTOK.PREFERRED_KEYWORDS),
		PreferredHashtags: getVal(row, INDEX_DATA_TIKTOK.PREFERRED_HASHTAGS),
		WritingStyle:      getVal(row, INDEX_DATA_TIKTOK.WRITING_STYLE),
		MainGoal:          getVal(row, INDEX_DATA_TIKTOK.MAIN_GOAL),
		DefaultCta:        getVal(row, INDEX_DATA_TIKTOK.DEFAULT_CTA),
		ContentLength:     getVal(row, INDEX_DATA_TIKTOK.CONTENT_LENGTH),
		ContentType:       getVal(row, INDEX_DATA_TIKTOK.CONTENT_TYPE),
		TargetAudience:    getVal(row, INDEX_DATA_TIKTOK.TARGET_AUDIENCE),
		VisualStyle:       getVal(row, INDEX_DATA_TIKTOK.VISUAL_STYLE),
		AiPersona:         getVal(row, INDEX_DATA_TIKTOK.AI_PERSONA),
		BannedKeywords:    getVal(row, INDEX_DATA_TIKTOK.BANNED_KEYWORDS),
		ContentLanguage:   getVal(row, INDEX_DATA_TIKTOK.CONTENT_LANGUAGE),
		Country:           getVal(row, INDEX_DATA_TIKTOK.COUNTRY),
	}
}

func ConvertSerialDate(v interface{}) int64 {
	// ... (Giữ nguyên logic cũ nếu cần) ...
	return 0
}
