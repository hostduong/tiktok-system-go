package models

import (
	"fmt"
	"strings"
)

// TikTokAccount đại diện cho 1 dòng dữ liệu trong Sheet (61 cột)
type TikTokAccount struct {
	// Metadata
	RowIndex int `json:"row_index"`

	// --- NHÓM 1: AUTH & INFO (Cột 0 -> 22) ---
	Status        string `json:"status" col:"0"`
	Note          string `json:"note" col:"1"`
	DeviceId      string `json:"device_id" col:"2"`
	UserId        string `json:"user_id" col:"3"`
	UserSec       string `json:"user_sec" col:"4"`
	UserName      string `json:"user_name" col:"5"`
	Email         string `json:"email" col:"6"`
	NickName      string `json:"nick_name" col:"7"`
	Password      string `json:"password" col:"8"`
	PasswordEmail string `json:"password_email" col:"9"`
	RecoveryEmail string `json:"recovery_email" col:"10"`
	TwoFA         string `json:"two_fa" col:"11"`
	Phone         string `json:"phone" col:"12"`
	Birthday      string `json:"birthday" col:"13"`
	ClientId      string `json:"client_id" col:"14"`
	RefreshToken  string `json:"refresh_token" col:"15"`
	AccessToken   string `json:"access_token" col:"16"`
	Cookie        string `json:"cookie" col:"17"`
	UserAgent     string `json:"user_agent" col:"18"`
	Proxy         string `json:"proxy" col:"19"`
	ProxyExpired  string `json:"proxy_expired" col:"20"`
	CreateCountry string `json:"create_country" col:"21"`
	CreateTime    string `json:"create_time" col:"22"`

	// --- NHÓM 2: ACTIVITY & STATS (Cột 23 -> 44) ---
	StatusPost       string `json:"status_post" col:"23"`
	DailyPostLimit   string `json:"daily_post_limit" col:"24"`
	TodayPostCount   string `json:"today_post_count" col:"25"`
	DailyFollowLimit string `json:"daily_follow_limit" col:"26"`
	TodayFollowCount string `json:"today_follow_count" col:"27"`
	LastActiveDate   string `json:"last_active_date" col:"28"`
	FollowerCount    string `json:"follower_count" col:"29"`
	FollowingCount   string `json:"following_count" col:"30"`
	LikesCount       string `json:"likes_count" col:"31"`
	VideoCount       string `json:"video_count" col:"32"`
	StatusLive       string `json:"status_live" col:"33"`
	LivePhoneAccess  string `json:"live_phone_access" col:"34"`
	LiveStudioAccess string `json:"live_studio_access" col:"35"`
	LiveKey          string `json:"live_key" col:"36"`
	LastLiveDuration string `json:"last_live_duration" col:"37"`
	ShopRole         string `json:"shop_role" col:"38"`
	ShopId           string `json:"shop_id" col:"39"`
	ProductCount     string `json:"product_count" col:"40"`
	ShopHealth       string `json:"shop_health" col:"41"`
	TotalOrders      string `json:"total_orders" col:"42"`
	TotalRevenue     string `json:"total_revenue" col:"43"`
	CommissionRate   string `json:"commission_rate" col:"44"`

	// --- NHÓM 3: AI PERSONA & SETTINGS (Cột 45 -> 60) ---
	Signature         string `json:"signature" col:"45"`
	DefaultCategory   string `json:"default_category" col:"46"`
	DefaultProduct    string `json:"default_product" col:"47"`
	PreferredKeywords string `json:"preferred_keywords" col:"48"`
	PreferredHashtags string `json:"preferred_hashtags" col:"49"`
	WritingStyle      string `json:"writing_style" col:"50"`
	MainGoal          string `json:"main_goal" col:"51"`
	DefaultCTA        string `json:"default_cta" col:"52"`
	ContentLength     string `json:"content_length" col:"53"`
	ContentType       string `json:"content_type" col:"54"`
	TargetAudience    string `json:"target_audience" col:"55"`
	VisualStyle       string `json:"visual_style" col:"56"`
	AIPersona         string `json:"ai_persona" col:"57"`
	BannedKeywords    string `json:"banned_keywords" col:"58"`
	ContentLanguage   string `json:"content_language" col:"59"`
	Country           string `json:"country" col:"60"`

	// --- QUAN TRỌNG: Để tránh lỗi biên dịch trong update.go ---
	ExtraData []string `json:"-"`
}

// NewAccount tạo một account rỗng
func NewAccount() *TikTokAccount {
	return &TikTokAccount{
		ExtraData: make([]string, 61), // Khởi tạo mảng đệm
	}
}

// ToSlice chuyển đổi Struct thành mảng []interface{}
func (a *TikTokAccount) ToSlice() []interface{} {
	// Cập nhật lại ExtraData từ các trường struct (Sync ngược)
	// (Logic đơn giản: tạo mảng mới từ các trường hiện tại)
	return []interface{}{
		a.Status, a.Note, a.DeviceId, a.UserId, a.UserSec, a.UserName, a.Email,
		a.NickName, a.Password, a.PasswordEmail, a.RecoveryEmail, a.TwoFA, a.Phone,
		a.Birthday, a.ClientId, a.RefreshToken, a.AccessToken, a.Cookie, a.UserAgent,
		a.Proxy, a.ProxyExpired, a.CreateCountry, a.CreateTime,
		a.StatusPost, a.DailyPostLimit, a.TodayPostCount, a.DailyFollowLimit, a.TodayFollowCount,
		a.LastActiveDate, a.FollowerCount, a.FollowingCount, a.LikesCount, a.VideoCount,
		a.StatusLive, a.LivePhoneAccess, a.LiveStudioAccess, a.LiveKey, a.LastLiveDuration,
		a.ShopRole, a.ShopId, a.ProductCount, a.ShopHealth, a.TotalOrders, a.TotalRevenue, a.CommissionRate,
		a.Signature, a.DefaultCategory, a.DefaultProduct, a.PreferredKeywords, a.PreferredHashtags,
		a.WritingStyle, a.MainGoal, a.DefaultCTA, a.ContentLength, a.ContentType,
		a.TargetAudience, a.VisualStyle, a.AIPersona, a.BannedKeywords, a.ContentLanguage, a.Country,
	}
}

// FromSlice map dữ liệu từ mảng (Sheet) vào Struct
func (a *TikTokAccount) FromSlice(row []interface{}) {
	getString := func(i int) string {
		if i >= len(row) {
			return ""
		}
		return fmt.Sprintf("%v", row[i])
	}

	// Đổ dữ liệu vào ExtraData trước (để backup)
	a.ExtraData = make([]string, 61)
	for i := 0; i < 61; i++ {
		a.ExtraData[i] = getString(i)
	}

	// Map vào từng trường cụ thể
	a.Status = getString(0)
	a.Note = getString(1)
	a.DeviceId = getString(2)
	a.UserId = getString(3)
	a.UserSec = getString(4)
	a.UserName = getString(5)
	a.Email = getString(6)
	a.NickName = getString(7)
	a.Password = getString(8)
	a.PasswordEmail = getString(9)
	a.RecoveryEmail = getString(10)
	a.TwoFA = getString(11)
	a.Phone = getString(12)
	a.Birthday = getString(13)
	a.ClientId = getString(14)
	a.RefreshToken = getString(15)
	a.AccessToken = getString(16)
	a.Cookie = getString(17)
	a.UserAgent = getString(18)
	a.Proxy = getString(19)
	a.ProxyExpired = getString(20)
	a.CreateCountry = getString(21)
	a.CreateTime = getString(22)

	a.StatusPost = getString(23)
	a.DailyPostLimit = getString(24)
	a.TodayPostCount = getString(25)
	a.DailyFollowLimit = getString(26)
	a.TodayFollowCount = getString(27)
	a.LastActiveDate = getString(28)
	a.FollowerCount = getString(29)
	a.FollowingCount = getString(30)
	a.LikesCount = getString(31)
	a.VideoCount = getString(32)
	a.StatusLive = getString(33)
	a.LivePhoneAccess = getString(34)
	a.LiveStudioAccess = getString(35)
	a.LiveKey = getString(36)
	a.LastLiveDuration = getString(37)
	a.ShopRole = getString(38)
	a.ShopId = getString(39)
	a.ProductCount = getString(40)
	a.ShopHealth = getString(41)
	a.TotalOrders = getString(42)
	a.TotalRevenue = getString(43)
	a.CommissionRate = getString(44)

	a.Signature = getString(45)
	a.DefaultCategory = getString(46)
	a.DefaultProduct = getString(47)
	a.PreferredKeywords = getString(48)
	a.PreferredHashtags = getString(49)
	a.WritingStyle = getString(50)
	a.MainGoal = getString(51)
	a.DefaultCTA = getString(52)
	a.ContentLength = getString(53)
	a.ContentType = getString(54)
	a.TargetAudience = getString(55)
	a.VisualStyle = getString(56)
	a.AIPersona = getString(57)
	a.BannedKeywords = getString(58)
	a.ContentLanguage = getString(59)
	a.Country = getString(60)
}
