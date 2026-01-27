package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// NormalizeString: Lowercase + Trim + Remove Tiếng Việt accents
func NormalizeString(s string) string {
	t := transform.Chain(norm.NFD, transform.RemoveFunc(func(r rune) bool {
		return unicode.Is(unicode.Mn, r) // Remove dấu
	}), norm.NFC)
	result, _, _ := transform.String(t, s)
	return strings.ToLower(strings.TrimSpace(result))
}

func GetVietnamTime() string {
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	return time.Now().In(loc).Format("02/01/2006 15:04:05")
}

var regexCount = regexp.MustCompile(`\(Lần\s*(\d+)\)`)
var regexDate = regexp.MustCompile(`(\d{1,2}\/\d{1,2}\/\d{4})`)

func CreateStandardNote(oldNote, newStatus, mode string) string {
	nowFull := GetVietnamTime()
	
	if mode == "new" {
		if newStatus == "" { newStatus = "Đang chờ" }
		return fmt.Sprintf("%s\n%s", newStatus, nowFull)
	}

	// Parse Count
	count := 0
	matches := regexCount.FindStringSubmatch(oldNote)
	if len(matches) > 1 {
		count, _ = strconv.Atoi(matches[1])
	}

	if mode == "updated" {
		if count == 0 { count = 1 }
		statusToUse := newStatus
		if statusToUse == "" {
			parts := strings.Split(oldNote, "\n")
			if len(parts) > 0 { statusToUse = parts[0] } else { statusToUse = "Đang chạy" }
		}
		return fmt.Sprintf("%s\n%s (Lần %d)", statusToUse, nowFull, count)
	}

	// Logic Reset Check Date
	todayStr := strings.Split(nowFull, " ")[0]
	dateMatch := regexDate.FindStringSubmatch(oldNote)
	oldDate := ""
	if len(dateMatch) > 1 { oldDate = dateMatch[1] }
	
	if oldDate != todayStr {
		count = 1
	} else {
		if mode == "reset" { count++ } else if count == 0 { count = 1 }
	}
	
	return fmt.Sprintf("%s\n%s (Lần %d)", newStatus, nowFull, count)
}

func JSONResponse(w http.ResponseWriter, status, msg string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"status": status, "messenger": msg,
	}
	json.NewEncoder(w).Encode(resp)
}

func JSONResponseRaw(w http.ResponseWriter, data map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
