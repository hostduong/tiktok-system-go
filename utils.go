package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm" // Cần import thư viện chuẩn hóa text
)

// Làm sạch chuỗi (Trim, Lowercase, NFC)
func CleanString(val interface{}) string {
	if val == nil {
		return ""
	}
	str := fmt.Sprintf("%v", val)
	// Chuẩn hóa NFC (tương đương normalize('NFC') trong JS)
	str = norm.NFC.String(str)
	return strings.ToLower(strings.TrimSpace(str))
}

// Chặn Formula Injection (Thêm dấu ' nếu bắt đầu bằng =, +, -, @)
func CleanForSheet(val interface{}) interface{} {
	str, ok := val.(string)
	if !ok {
		return val
	}
	// Xóa ký tự điều khiển
	// (Logic regex replace control chars có thể thêm nếu cần thiết)
	
	if len(str) > 0 && (str[0] == '=' || str[0] == '+' || str[0] == '-' || str[0] == '@') {
		return "'" + str
	}
	return str
}

// Lấy giờ Việt Nam (UTC+7)
func GetTimeVN() string {
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh") // Hoặc dùng FixedZone
	if loc == nil {
		loc = time.FixedZone("UTC+7", 7*60*60)
	}
	now := time.Now().In(loc)
	return fmt.Sprintf("%02d/%02d/%04d %02d:%02d:%02d",
		now.Day(), int(now.Month()), now.Year(),
		now.Hour(), now.Minute(), now.Second())
}

// Logic tạo Ghi Chú (Note) chuẩn - Giống hệt Node.js
func CreateStandardNote(oldNote string, newStatus string, mode string) string {
	oldNoteNorm := norm.NFC.String(oldNote)
	nowFull := GetTimeVN()
	
	if mode == "new" {
		status := "Đang chờ"
		if newStatus != "" {
			status = newStatus
		}
		return fmt.Sprintf("%s\n%s", status, nowFull)
	}

	count := 0
	match := REGEX_COUNT.FindStringSubmatch(oldNoteNorm)
	if len(match) > 1 {
		c, err := strconv.Atoi(match[1])
		if err == nil {
			count = c
		}
	}

	if mode == "updated" {
		if count == 0 {
			count = 1
		}
		statusToUse := newStatus
		if statusToUse == "" {
			parts := strings.Split(oldNoteNorm, "\n")
			if len(parts) > 0 {
				statusToUse = parts[0]
			} else {
				statusToUse = "Đang chạy"
			}
		}
		return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
	}

	// Mode reset or normal
	todayStr := strings.Split(nowFull, " ")[0]
	
	oldDate := ""
	dateMatch := REGEX_DATE.FindStringSubmatch(oldNoteNorm)
	if len(dateMatch) > 1 {
		oldDate = strings.TrimSpace(dateMatch[1])
	}

	if oldDate != todayStr {
		count = 1
	} else {
		if mode == "reset" {
			count++
		} else if count == 0 {
			count = 1
		}
	}
	
	return fmt.Sprintf("%s\n%s (Lần %d)", newStatus, nowFull, count)
}

// Chuyển đổi Serial Date Excel sang Time Golang
func ConvertSerialDate(v interface{}) int64 {
	if v == nil {
		return 0
	}
	
	// Nếu là số (Serial Date của Excel)
	if f, ok := v.(float64); ok {
		// Excel base date: Dec 30 1899
		// Logic Node.js: (v - 25569) * 86400000 - (7 * 3600000)
		// Go xử lý tương tự
		return int64((f - 25569) * 86400000) - (7 * 3600000)
	}
	
	// Nếu là chuỗi ngày tháng
	str := fmt.Sprintf("%v", v)
	parts := strings.FieldsFunc(str, func(r rune) bool {
		return r == '/' || r == ' ' || r == ':' || r == '-'
	})
	
	if len(parts) >= 3 {
		day, _ := strconv.Atoi(parts[0])
		month, _ := strconv.Atoi(parts[1])
		year, _ := strconv.Atoi(parts[2])
		hour, min, sec := 0, 0, 0
		if len(parts) >= 4 { hour, _ = strconv.Atoi(parts[3]) }
		if len(parts) >= 5 { min, _ = strconv.Atoi(parts[4]) }
		if len(parts) >= 6 { sec, _ = strconv.Atoi(parts[5]) }
		
		t := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC)
		return t.UnixMilli() - (7 * 3600000) // Trừ 7 tiếng để về logic cũ
	}
	
	return 0
}
