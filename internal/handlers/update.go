package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tiktok-server/internal/cache"
	"tiktok-server/internal/models"
	"tiktok-server/internal/queue"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

type UpdatedRequest struct {
	Type     string                 `json:"type"`
	Token    string                 `json:"token"`
	DeviceId string                 `json:"deviceId"`
	RowIndex int                    `json:"row_index"`
	Sheet    string                 `json:"sheet"`
	Note     string                 `json:"note"`
	Extra    map[string]interface{} `json:"-"`
}

func (r *UpdatedRequest) UnmarshalJSON(data []byte) error {
	type Alias UpdatedRequest
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	r.Extra = m
	return nil
}

func HandleUpdate(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	var body UpdatedRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}

	sheetName := body.Sheet
	if sheetName == "" {
		sheetName = "DataTiktok"
	}
	isDataTiktok := (sheetName == "DataTiktok")

	var cacheItem *cache.SheetCacheItem
	val, ok := cache.GlobalSheets.Load(spreadsheetId + "__" + sheetName)
	if ok {
		cacheItem = val.(*cache.SheetCacheItem)
	}

	if cacheItem == nil || !cacheItem.IsValid() {
		cacheItem = cache.NewSheetCache(spreadsheetId, sheetName)
		accounts, err := sheetSvc.FetchData(spreadsheetId, sheetName, 11, 10000)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"status":"false","messenger":"Lỗi tải dữ liệu: %v"}`, err), 500)
			return
		}
		cacheItem.Lock()
		cacheItem.RawValues = accounts
		cacheItem.Unlock()
		cache.GlobalSheets.Store(spreadsheetId+"__"+sheetName, cacheItem)
	}

	targetIndex := -1
	isAppend := false

	cacheItem.RLock()
	rows := cacheItem.RawValues

	if body.RowIndex >= 11 {
		idx := body.RowIndex - 11
		if idx >= 0 && idx < len(rows) {
			targetIndex = idx
		} else {
			cacheItem.RUnlock()
			utils.JSONResponse(w, "false", "Dòng yêu cầu không tồn tại", nil)
			return
		}
	} else {
		searchCols := make(map[int]string)
		for k, v := range body.Extra {
			if strings.HasPrefix(k, "search_col_") {
				idx, _ := strconv.Atoi(strings.TrimPrefix(k, "search_col_"))
				searchCols[idx] = utils.NormalizeString(fmt.Sprintf("%v", v))
			}
		}

		if len(searchCols) > 0 {
			for i, acc := range rows {
				match := true
				rawArr := acc.ToSlice()
				for colIdx, val := range searchCols {
					cellVal := ""
					if colIdx < len(rawArr) {
						cellVal = utils.NormalizeString(fmt.Sprintf("%v", rawArr[colIdx]))
					}
					if cellVal != val {
						match = false
						break
					}
				}
				if match {
					targetIndex = i
					break
				}
			}
			if targetIndex == -1 {
				cacheItem.RUnlock()
				utils.JSONResponse(w, "false", "Không tìm thấy nick phù hợp", nil)
				return
			}
		} else {
			isAppend = true
		}
	}
	cacheItem.RUnlock()

	var newAcc *models.TikTokAccount
	var oldNote string

	if isAppend {
		newAcc = models.NewAccount()
	} else {
		oldAcc := cacheItem.GetAccountByIndex(targetIndex)
		temp := *oldAcc
		newAcc = &temp
		newAcc.ExtraData = make([]string, len(oldAcc.ExtraData))
		copy(newAcc.ExtraData, oldAcc.ExtraData)
		oldNote = oldAcc.Note
	}

	currentSlice := newAcc.ToSlice()
	for len(currentSlice) < 61 {
		currentSlice = append(currentSlice, "")
	}

	for k, v := range body.Extra {
		if strings.HasPrefix(k, "col_") {
			idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
			if idx >= 0 && idx < 61 {
				currentSlice[idx] = fmt.Sprintf("%v", v)
			}
		}
	}

	if isDataTiktok {
		content := body.Note
		if content == "" {
			if v, ok := body.Extra["col_1"]; ok {
				content = fmt.Sprintf("%v", v)
			}
		}

		noteMode := "updated"
		if isAppend {
			noteMode = "new"
		}

		newNote := utils.CreateStandardNote(oldNote, content, noteMode)
		currentSlice[1] = newNote

		if body.DeviceId != "" {
			currentSlice[2] = body.DeviceId
		}
	}

	newAcc.FromSlice(currentSlice)

	q := queue.GetQueue(spreadsheetId, sheetSvc)

	if isAppend {
		cacheItem.Lock()
		newAcc.RowIndex = 11 + len(cacheItem.RawValues)
		cacheItem.RawValues = append(cacheItem.RawValues, newAcc)
		cacheItem.LastAccessed = time.Now()
		cacheItem.Unlock()

		q.EnqueueAppend(sheetName, newAcc.ToSlice())

		auth, activity, ai := SplitProfile(newAcc)
		resp := map[string]interface{}{
			"status":           "true",
			"type":             "updated",
			"messenger":        "Thêm mới thành công",
			"auth_profile":     auth,
			"activity_profile": activity,
			"ai_profile":       ai,
		}
		utils.JSONResponseRaw(w, resp)
	} else {
		cacheItem.UpdateAccount(targetIndex, newAcc)
		q.EnqueueUpdate(sheetName, targetIndex, newAcc)

		auth, activity, ai := SplitProfile(newAcc)
		resp := map[string]interface{}{
			"status":           "true",
			"type":             "updated",
			"messenger":        "Cập nhật thành công",
			"row_index":        11 + targetIndex,
			"auth_profile":     auth,
			"activity_profile": activity,
			"ai_profile":       ai,
		}
		utils.JSONResponseRaw(w, resp)
	}
}
