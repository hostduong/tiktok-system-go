package handlers

import (
	"tiktok-server/internal/models"
)

// SplitProfile: Tách 1 account thành 3 nhóm dữ liệu để trả về Client
func SplitProfile(acc *models.TikTokAccount) (map[string]interface{}, map[string]interface{}, map[string]interface{}) {
	
	// Nhóm 1: Auth & Info
	authProfile := map[string]interface{}{
		"status":           acc.Status,
		"note":             acc.Note,
		"device_id":        acc.DeviceId,
		"user_id":          acc.UserId,
		"user_sec":         acc.UserSec,
		"user_name":        acc.UserName,
		"email":            acc.Email,
		"nick_name":        acc.NickName,
		"password":         acc.Password,
		"password_email":   acc.PasswordEmail,
		"recovery_email":   acc.RecoveryEmail,
		"two_fa":           acc.TwoFA,
		"phone":            acc.Phone,
		"birthday":         acc.Birthday,
		"client_id":        acc.ClientId,
		"refresh_token":    acc.RefreshToken,
		"access_token":     acc.AccessToken,
		"cookie":           acc.Cookie,
		"user_agent":       acc.UserAgent,
		"proxy":            acc.Proxy,
		"proxy_expired":    acc.ProxyExpired,
		"create_country":   acc.CreateCountry,
		"create_time":      acc.CreateTime,
	}

	// Nhóm 2: Activity & Stats
	activityProfile := map[string]interface{}{
		"status_post":        acc.StatusPost,
		"daily_post_limit":   acc.DailyPostLimit,
		"today_post_count":   acc.TodayPostCount,
		"daily_follow_limit": acc.DailyFollowLimit,
		"today_follow_count": acc.TodayFollowCount,
		"last_active_date":   acc.LastActiveDate,
		"follower_count":     acc.FollowerCount,
		"following_count":    acc.FollowingCount,
		"likes_count":        acc.LikesCount,
		"video_count":        acc.VideoCount,
		"status_live":        acc.StatusLive,
		"live_phone_access":  acc.LivePhoneAccess,
		"live_studio_access": acc.LiveStudioAccess,
		"live_key":           acc.LiveKey,
		"last_live_duration": acc.LastLiveDuration,
		"shop_role":          acc.ShopRole,
		"shop_id":            acc.ShopId,
		"product_count":      acc.ProductCount,
		"shop_health":        acc.ShopHealth,
		"total_orders":       acc.TotalOrders,
		"total_revenue":      acc.TotalRevenue,
		"commission_rate":    acc.CommissionRate,
	}

	// Nhóm 3: AI & Settings
	aiProfile := map[string]interface{}{
		"signature":          acc.Signature,
		"default_category":   acc.DefaultCategory,
		"default_product":    acc.DefaultProduct,
		"preferred_keywords": acc.PreferredKeywords,
		"preferred_hashtags": acc.PreferredHashtags,
		"writing_style":      acc.WritingStyle,
		"main_goal":          acc.MainGoal,
		"default_cta":        acc.DefaultCTA,
		"content_length":     acc.ContentLength,
		"content_type":       acc.ContentType,
		"target_audience":    acc.TargetAudience,
		"visual_style":       acc.VisualStyle,
		"ai_persona":         acc.AIPersona,
		"banned_keywords":    acc.BannedKeywords,
		"content_language":   acc.ContentLanguage,
		"country":            acc.Country,
	}

	return authProfile, activityProfile, aiProfile
}
