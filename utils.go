package main

import (
	"fmt"
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

// 🔥 Port từ Node.js: chuyen_doi_thoi_gian
func ConvertSerialDate(v interface{}) int64 {
	s := fmt.Sprintf("%v", v)
	
	// Case 1: String Date (dd/mm/yyyy)
	if strings.Contains(s, "/") {
		// Thử format có giờ
		if t, err := time.ParseInLocation("02/01/2006 15:04:05", s, time.FixedZone("UTC+7", 7*3600)); err == nil {
			return t.UnixMilli()
		}
		// Thử format ngày
		if t, err := time.ParseInLocation("02/01/2006", s, time.FixedZone("UTC+7", 7*3600)); err == nil {
			return t.UnixMilli()
		}
	}

	// Case 2: Excel Serial Number (45321.123)
	val := 0.0
	if f, ok := v.(float64); ok {
		val = f
	} else if f, err := strconv.ParseFloat(s, 64); err == nil {
		val = f
	}

	if val > 0 {
		// Excel epoch: Dec 30, 1899
		t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		days := int(val)
		seconds := int((val - float64(days)) * 86400)
		return t.AddDate(0, 0, days).Add(time.Duration(seconds) * time.Second).UnixMilli()
	}
	
	return 0
}

// ... (Giữ nguyên các struct AuthProfile, ActivityProfile, AiProfile...)
type AuthProfile struct {
	UID string `json:"uid"` 
	Email string `json:"email"` 
	Password string `json:"password"` 
	User string `json:"user"` 
	TwoFA string `json:"2fa"` 
	Cookie string `json:"cookie"` 
	Token string `json:"token"`
}
type ActivityProfile struct {
	LastActive string `json:"last_active"` 
	PostCount string `json:"post_count"` 
	Follower string `json:"follower"`
}
type AiProfile struct {
	Signature string `json:"signature"` 
	Persona string `json:"persona"` 
	Target string `json:"target"`
}

func MakeAuthProfile(row []interface{}) AuthProfile {
	return AuthProfile{
		UID: getString(row, INDEX_DATA_TIKTOK.USER_ID), Email: getString(row, INDEX_DATA_TIKTOK.EMAIL), Password: getString(row, INDEX_DATA_TIKTOK.PASSWORD),
		User: getString(row, INDEX_DATA_TIKTOK.USER_NAME), TwoFA: getString(row, INDEX_DATA_TIKTOK.TWO_FA), Cookie: getString(row, INDEX_DATA_TIKTOK.COOKIE), Token: getString(row, INDEX_DATA_TIKTOK.ACCESS_TOKEN),
	}
}
func MakeActivityProfile(row []interface{}) ActivityProfile {
	return ActivityProfile{ LastActive: getString(row, INDEX_DATA_TIKTOK.LAST_ACTIVE_DATE), PostCount: getString(row, INDEX_DATA_TIKTOK.VIDEO_COUNT), Follower: getString(row, INDEX_DATA_TIKTOK.FOLLOWER_COUNT), }
}
func MakeAiProfile(row []interface{}) AiProfile {
	return AiProfile{ Signature: getString(row, INDEX_DATA_TIKTOK.SIGNATURE), Persona: getString(row, INDEX_DATA_TIKTOK.AI_PERSONA), Target: getString(row, INDEX_DATA_TIKTOK.TARGET_AUDIENCE), }
}
func getString(row []interface{}, idx int) string {
	if idx >= 0 && idx < len(row) { return fmt.Sprintf("%v", row[idx]) }
	return ""
}
