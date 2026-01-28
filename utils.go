package main

import (
	"fmt"
	"strings"
)

// CleanString: Chuẩn hóa chuỗi (Trim + Lowercase)
func CleanString(v interface{}) string {
	if v == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
}

// SafeString: Lấy chuỗi an toàn (Trim only)
func SafeString(v interface{}) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// =================================================================================================
// 📦 CÁC STRUCT PROFILE & CONVERTER
// =================================================================================================

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

// Tạo AuthProfile từ dòng dữ liệu
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

// Helper lấy string từ mảng interface an toàn
func getString(row []interface{}, idx int) string {
	if idx >= 0 && idx < len(row) {
		return fmt.Sprintf("%v", row[idx])
	}
	return ""
}
