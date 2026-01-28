package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"
)

type LoginResponse struct {
	Status          string            `json:"status"`
	Type            string            `json:"type"`
	Messenger       string            `json:"messenger"`
	DeviceId        string            `json:"deviceId"`
	RowIndex        int               `json:"row_index"`
	SystemEmail     string            `json:"system_email"`
	AuthProfile     map[string]string `json:"auth_profile"`
	ActivityProfile map[string]string `json:"activity_profile"`
	AiProfile       map[string]string `json:"ai_profile"`
}

var INDEX_TO_KEY map[int]string

func init() {
	INDEX_TO_KEY = make(map[int]string)
	val := reflect.ValueOf(INDEX_DATA_TIKTOK)
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		keyName := strings.ToLower(typ.Field(i).Name)
		idx := int(val.Field(i).Int())
		INDEX_TO_KEY[idx] = keyName
	}
}

// 🔥 Đổi tên hàm thành HandleAccountAction để khớp với main.go của bạn
func HandleAccountAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi Body JSON"}`, 400)
		return
	}

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok {
		http.Error(w, `{"status":"false","messenger":"Lỗi xác thực"}`, 401)
		return
	}

	// 🔥 FIX: SpreadsheetID viết hoa chữ D
	spreadsheetId := tokenData.SpreadsheetID
	deviceId := CleanString(body["deviceId"])
	reqType := CleanString(body["type"])
	
	action := "login"
	if reqType == "view" {
		action = "view_only"
	} else if reqType == "auto" {
		action = "auto"
		if CleanString(body["action"]) == "reset" {
			body["is_reset"] = true
		}
	} else if reqType == "register" {
		action = "register"
	} else if CleanString(body["action"]) == "reset" {
		action = "login_reset"
	}

	res, err := xu_ly_lay_du_lieu(spreadsheetId, deviceId, body, action)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func xu_ly_lay_du_lieu(sid, deviceId string, body map[string]interface{}, action string) (*LoginResponse, error) {
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.DATA_TIKTOK, false)
	if err != nil { return nil, fmt.Errorf("Lỗi tải dữ liệu") }

	allData := cacheData.RawValues
	cleanValues := cacheData.CleanValues
	targetIndex := -1
	targetData := make([]interface{}, 61)
	responseType := "login"
	sysEmail := ""
	var cleanupIndices []int
	var badIndices []map[string]interface{}

	reqRowIndex := -1
	if v, ok := body["row_index"].(float64); ok { reqRowIndex = int(v) }
	
	isFast := false
	if reqRowIndex >= RANGES.DATA_START_ROW {
		idx := reqRowIndex - RANGES.DATA_START_ROW
		if idx >= 0 && idx < len(allData) {
			clean := cleanValues[idx]
			s_uid := CleanString(body["search_user_id"])
			match := (s_uid == "") || (clean[INDEX_DATA_TIKTOK.USER_ID] == s_uid)
			if match {
				targetIndex = idx
				targetData = allData[idx]
				isFast = true
				sysEmail = extractEmail(clean[INDEX_DATA_TIKTOK.EMAIL])
			}
		}
	}

	if !isFast {
		searchRes := simpleSearch(cacheData, action, deviceId)
		targetIndex = searchRes.TargetIndex
		if targetIndex == -1 { return nil, fmt.Errorf("Không còn tài khoản phù hợp") }
		targetData = allData[targetIndex]
		responseType = searchRes.ResponseType
		sysEmail = searchRes.SystemEmail
	}

	if action == "view_only" {
		return buildResponse(targetData, targetIndex, responseType, "OK", deviceId, sysEmail), nil
	}

	tSt := STATUS_WRITE.RUNNING
	if responseType == "register" { tSt = STATUS_WRITE.REGISTERING }
	tNote := fmt.Sprintf("%s\n%s", tSt, time.Now().Add(7*time.Hour).Format("02/01/2006 15:04:05"))

	newRow := make([]interface{}, len(targetData))
	copy(newRow, targetData)
	newRow[INDEX_DATA_TIKTOK.STATUS] = tSt
	newRow[INDEX_DATA_TIKTOK.DEVICE_ID] = deviceId
	newRow[INDEX_DATA_TIKTOK.NOTE] = tNote

	QueueUpdate(sid, SHEET_NAMES.DATA_TIKTOK, targetIndex, newRow)

	if len(cleanupIndices) > 0 {}
	if len(badIndices) > 0 {}

	msg := "Lấy nick đăng nhập thành công"
	if responseType == "register" { msg = "Lấy nick đăng ký thành công" }

	return buildResponse(newRow, targetIndex, responseType, msg, deviceId, sysEmail), nil
}

type SearchResult struct {
	TargetIndex  int
	ResponseType string
	SystemEmail  string
	BadIndices   []map[string]interface{}
}

func simpleSearch(cache *SheetCacheData, action, devId string) SearchResult {
	for i, row := range cache.CleanValues {
		curDev := row[INDEX_DATA_TIKTOK.DEVICE_ID]
		st := row[INDEX_DATA_TIKTOK.STATUS]
		isMy := (curDev == devId)
		isEmpty := (curDev == "")
		isLoginSt := (st == "đang chạy" || st == "đang chờ" || st == "đăng nhập")
		if (isMy || isEmpty) && isLoginSt {
			return SearchResult{TargetIndex: i, ResponseType: "login", SystemEmail: extractEmail(row[INDEX_DATA_TIKTOK.EMAIL])}
		}
	}
	return SearchResult{TargetIndex: -1}
}

func extractEmail(raw string) string {
	if strings.Contains(raw, "@") { parts := strings.Split(raw, "@"); if len(parts) > 1 { return parts[1] } }
	return ""
}

func buildResponse(row []interface{}, idx int, typ, msg, devId, email string) *LoginResponse {
	return &LoginResponse{
		Status: "true", Type: typ, Messenger: msg, DeviceId: devId,
		RowIndex: RANGES.DATA_START_ROW + idx, SystemEmail: email,
		AuthProfile: mapProfileSafe(row, 0, 22),
		ActivityProfile: mapProfileSafe(row, 23, 44),
		AiProfile: mapProfileSafe(row, 45, 60),
	}
}

func mapProfileSafe(row []interface{}, start, end int) map[string]string {
	res := make(map[string]string)
	for i := start; i <= end; i++ {
		key := INDEX_TO_KEY[i]
		if key != "" {
			if i < len(row) { res[key] = SafeString(row[i]) } else { res[key] = "" }
		}
	}
	return res
}

func SafeString(v interface{}) string {
	if v == nil { return "" }
	switch val := v.(type) {
	case string: return val
	case float64: if val == float64(int64(val)) { return fmt.Sprintf("%.0f", val) }; return fmt.Sprintf("%v", val)
	default: return fmt.Sprintf("%v", val)
	}
}
