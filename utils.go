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

// 🔥 HÀM QUAN TRỌNG: ConvertSerialDate (Port 100% từ Node.js chuyen_doi_thoi_gian)
// Fix lỗi undefined trong handler_mail.go và handler_search.go
func ConvertSerialDate(v interface{}) int64 {
	if v == nil { return 0 }
	
	// Case 1: Số (Excel Serial Date)
	// Node.js logic: (v - 25569) * 86400000 - (7 * 3600000)
	if f, ok := v.(float64); ok {
		return int64((f - 25569) * 86400000) - (7 * 3600000)
	}
	
	// Case 2: String
	s := fmt.Sprintf("%v", v)
	// Node.js logic: Date.UTC(...)
	// Golang: ParseInLocation với múi giờ UTC+7
	if strings.Contains(s, "/") || strings.Contains(s, "-") || strings.Contains(s, ":") {
		// Thử các format phổ biến
		layouts := []string{
			"02/01/2006 15:04:05",
			"02/01/2006",
			"2006-01-02 15:04:05",
		}
		loc := time.FixedZone("UTC+7", 7*3600)
		for _, l := range layouts {
			if t, err := time.ParseInLocation(l, s, loc); err == nil {
				return t.UnixMilli()
			}
		}
	}
	return 0
}

// --- STRUCT PROFILES ---
type AuthProfile struct {
	UID      string `json:"uid"`
	Email    string `json:"email"`
	Password string `json:"password"`
	User     string `json:"user"`
	TwoFA    string `json:"2fa"`
	Cookie   string `json:"cookie"`
	Token    string `json:"token"`
}
type ActivityProfile struct {
	LastActive string `json:"last_active"`
	PostCount  string `json:"post_count"`
	Follower   string `json:"follower"`
}
type AiProfile struct {
	Signature string `json:"signature"`
	Persona   string `json:"persona"`
	Target    string `json:"target"`
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
