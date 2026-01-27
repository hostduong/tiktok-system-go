package utils

import (
	"encoding/json" // <--- Đã thêm
	"fmt"
	"net/http"      // <--- Đã thêm
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// NormalizeString: Chuẩn hóa chuỗi
func NormalizeString(val string) string {
	if val == "" {
		return ""
	}
	s := strings.TrimSpace(val)
	s = strings.ToLower(s)
	t := transform.Chain(norm.NFD, transform.RemoveFunc(isMn), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

func isMn(r rune) bool {
	return unicode.Is(unicode.Mn, r)
}

// GetVietnamTime: Lấy giờ VN
func GetVietnamTime() string {
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	now := time.Now().In(loc)
	return now.Format("02/01/2006 15:04:05")
}

var (
	regexCount = regexp.MustCompile(`\(Lần\s*(\d+)\)`)
	regexDate  = regexp.MustCompile(`(\d{1,2}\/\d{1,2}\/\d{4})`)
)

// CreateStandardNote: Tạo ghi chú chuẩn
func CreateStandardNote(oldNote, newStatus, mode string) string {
	str := NormalizeString(oldNote)
	nowFull := GetVietnamTime()

	if mode == "new" {
		if newStatus == "" {
			newStatus = "Đang chờ"
		}
		return fmt.Sprintf("%s\n%s", newStatus, nowFull)
	}

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
			parts := strings.Split(str, "\n")
			if len(parts) > 0 {
				statusToUse = parts[0]
			} else {
				statusToUse = "Đang chạy"
			}
		}
		return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
	}

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

// --- JSON Response Helpers ---

func JSONResponse(w http.ResponseWriter, status, msg string, data map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"status":    status,
		"messenger": msg,
	}
	if data != nil {
		for k, v := range data {
			resp[k] = v
		}
	}
	json.NewEncoder(w).Encode(resp)
}

func JSONResponseRaw(w http.ResponseWriter, data map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
