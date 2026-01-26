package utils

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// NormalizeString: Chuẩn hóa chuỗi (Lowercase, Trim, NFC)
// Tương đương: val.trim().toLowerCase().normalize('NFC')
func NormalizeString(val string) string {
	if val == "" {
		return ""
	}
	// Trim space
	s := strings.TrimSpace(val)
	// Lowercase
	s = strings.ToLower(s)
	// Normalize NFC
	t := transform.Chain(norm.NFD, transform.RemoveFunc(isMn), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

func isMn(r rune) bool {
	return unicode.Is(unicode.Mn, r) // Mn: nonspacing marks
}

// GetVietnamTime: Lấy giờ VN (UTC+7)
// Tương đương: lay_gio_viet_nam
func GetVietnamTime() string {
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	now := time.Now().In(loc)
	return now.Format("02/01/2006 15:04:05")
}

// Regex cho hàm Note
var (
	regexCount = regexp.MustCompile(`\(Lần\s*(\d+)\)`)
	regexDate  = regexp.MustCompile(`(\d{1,2}\/\d{1,2}\/\d{4})`)
)

// CreateStandardNote: Tạo ghi chú chuẩn
// Tương đương: tao_ghi_chu_chuan
func CreateStandardNote(oldNote, newStatus, mode string) string {
	// Chuẩn hóa input
	str := NormalizeString(oldNote)
	nowFull := GetVietnamTime()

	if mode == "new" {
		if newStatus == "" {
			newStatus = "Đang chờ"
		}
		return fmt.Sprintf("%s\n%s", newStatus, nowFull)
	}

	// Lấy số lần chạy cũ
	count := 0
	matches := regexCount.FindStringSubmatch(str)
	if len(matches) > 1 {
		count, _ = strconv.Atoi(matches[1])
	}

	if mode == "updated" {
		if count == 0 {
			count = 1
		}
		statusToUse := newStatus
		if statusToUse == "" {
			// Lấy dòng đầu tiên của note cũ làm status
			parts := strings.Split(str, "\n")
			if len(parts) > 0 {
				statusToUse = parts[0]
			} else {
				statusToUse = "Đang chạy"
			}
		}
		return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
	}

	// Mode reset/login
	todayStr := strings.Split(nowFull, " ")[0]
	dateMatch := regexDate.FindStringSubmatch(str)
	oldDate := ""
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
