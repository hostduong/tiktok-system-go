package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func CleanString(v interface{}) string {
	if v == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
}

func SafeString(v interface{}) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// 🔥 BỔ SUNG HÀM NÀY (Để sửa lỗi undefined: ConvertSerialDate)
func ConvertSerialDate(v interface{}) int64 {
	// Nếu là chuỗi ngày tháng dạng "dd/mm/yyyy"
	s := fmt.Sprintf("%v", v)
	if strings.Contains(s, "/") {
		t, err := time.ParseInLocation("02/01/2006", s, time.FixedZone("UTC+7", 7*3600))
		if err == nil {
			return t.UnixMilli()
		}
		// Thử có giờ
		t2, err2 := time.ParseInLocation("02/01/2006 15:04:05", s, time.FixedZone("UTC+7", 7*3600))
		if err2 == nil {
			return t2.UnixMilli()
		}
	}

	// Nếu là số Serial Excel (ví dụ 45000.123)
	val, ok := 0.0, false
	if f, isFloat := v.(float64); isFloat {
		val = f
		ok = true
	} else if str, isString := v.(string); isString {
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			val = f
			ok = true
		}
	}

	if ok && val > 0 {
		// Excel base date: Dec 30, 1899
		base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		days := int(val)
		fraction := val - float64(days)
		seconds := int(fraction * 86400)
		
		t := base.AddDate(0, 0, days).Add(time.Duration(seconds) * time.Second)
		// Trừ đi 7 tiếng nếu Excel đang hiểu là Local Time, nhưng Server là UTC
		// Hoặc đơn giản trả về UnixMilli
		return t.UnixMilli()
	}
	
	return 0
}

// --- CÁC STRUCT PROFILE (GIỮ NGUYÊN) ---
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
		UID:      getString(row, INDEX_DATA_TIKTOK.USER_ID),
		Email:    getString(row, INDEX_DATA_TIKTOK.EMAIL),
		Password: getString(row, INDEX_DATA_TIKTOK.PASSWORD),
		User:     getString(row, INDEX_DATA_TIKTOK.USER_NAME),
		TwoFA:    getString(row, INDEX_DATA_TIKTOK.TWO_FA),
		Cookie:   getString(row, INDEX_DATA_TIKTOK.COOKIE),
		Token:    getString(row, INDEX_DATA_TIKTOK.ACCESS_TOKEN),
	}
}

func MakeActivityProfile(row []interface{}) ActivityProfile {
	return ActivityProfile{
		LastActive: getString(row, INDEX_DATA_TIKTOK.LAST_ACTIVE_DATE),
		PostCount:  getString(row, INDEX_DATA_TIKTOK.VIDEO_COUNT),
		Follower:   getString(row, INDEX_DATA_TIKTOK.FOLLOWER_COUNT),
	}
}

func MakeAiProfile(row []interface{}) AiProfile {
	return AiProfile{
		Signature: getString(row, INDEX_DATA_TIKTOK.SIGNATURE),
		Persona:   getString(row, INDEX_DATA_TIKTOK.AI_PERSONA),
		Target:    getString(row, INDEX_DATA_TIKTOK.TARGET_AUDIENCE),
	}
}

func getString(row []interface{}, idx int) string {
	if idx >= 0 && idx < len(row) {
		return fmt.Sprintf("%v", row[idx])
	}
	return ""
}
