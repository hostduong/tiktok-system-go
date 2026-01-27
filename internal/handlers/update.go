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

func HandleUpdate(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	// Parse JSON manual to map to handle flexible fields
	var bodyMap map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&bodyMap); err != nil {
		utils.JSONResponse(w, "false", "JSON Error", nil)
		return
	}

	sheetName := "DataTiktok"
	if s, ok := bodyMap["sheet"].(string); ok && s != "" {
		sheetName = s
	}
	isDataTiktok := (sheetName == "DataTiktok")
	deviceId, _ := bodyMap["deviceId"].(string)

	// Load Cache
	var cacheItem *cache.SheetCacheItem
	cacheKey := spreadsheetId + "__" + sheetName
	if val, ok := cache.GlobalSheets.Load(cacheKey); ok {
		cacheItem = val.(*cache.SheetCacheItem)
	}

	// Auto Load if missing
	if cacheItem == nil || !cacheItem.IsValid() {
		rawRows, err := sheetSvc.FetchData(spreadsheetId, sheetName, 11, 10000)
		if err != nil {
			utils.JSONResponse(w, "false", "Load error", nil)
			return
		}
		// Convert
		parsedAccounts := make([]*models.TikTokAccount, len(rawRows))
		for i, row := range rawRows {
			acc := models.NewAccount()
			acc.FromSlice(row)
			acc.RowIndex = 11 + i
			parsedAccounts[i] = acc
		}
		cacheItem = cache.NewSheetCache(spreadsheetId, sheetName)
		cacheItem.Lock()
		cacheItem.RawValues = parsedAccounts
		cacheItem.Unlock()
		cache.GlobalSheets.Store(cacheKey, cacheItem)
	}

	cacheItem.Lock()
	defer cacheItem.Unlock()

	// Logic tìm vị trí (Target Index)
	targetIndex := -1
	isAppend := false

	// 1. Tìm theo Row Index
	if ridx, ok := bodyMap["row_index"].(float64); ok { // JSON number is float64
		idx := int(ridx) - 11
		if idx >= 0 && idx < len(cacheItem.RawValues) {
			targetIndex = idx
		}
	}

	// 2. Tìm theo Search Columns
	if targetIndex == -1 {
		searchCols := make(map[int]string)
		for k, v := range bodyMap {
			if strings.HasPrefix(k, "search_col_") {
				idx, _ := strconv.Atoi(strings.TrimPrefix(k, "search_col_"))
				searchCols[idx] = utils.NormalizeString(fmt.Sprintf("%v", v))
			}
		}
		
		if len(searchCols) > 0 {
			for i, acc := range cacheItem.RawValues {
				match := true
				rowSlice := acc.ToSlice()
				for colIdx, searchVal := range searchCols {
					val := ""
					if colIdx < len(rowSlice) {
						val = utils.NormalizeString(fmt.Sprintf("%v", rowSlice[colIdx]))
					}
					if val != searchVal {
						match = false; break
					}
				}
				if match {
					targetIndex = i; break
				}
			}
			if targetIndex == -1 {
				utils.JSONResponse(w, "false", "Không tìm thấy nick", nil)
				return
			}
		} else {
			isAppend = true
		}
	}

	// Prepare Data
	var currentAcc *models.TikTokAccount
	if isAppend {
		currentAcc = models.NewAccount()
	} else {
		// Clone để tránh lỗi tham chiếu
		old := cacheItem.RawValues[targetIndex]
		cp := *old
		cp.ExtraData = make([]string, len(old.ExtraData))
		copy(cp.ExtraData, old.ExtraData)
		currentAcc = &cp
	}
	
	rowSlice := currentAcc.ToSlice()
	// Mở rộng slice nếu cần (V243 Nodejs fill 61)
	for len(rowSlice) < 61 { rowSlice = append(rowSlice, "") }

	// Update Values from "col_X"
	for k, v := range bodyMap {
		if strings.HasPrefix(k, "col_") {
			idx, _ := strconv.Atoi(strings.TrimPrefix(k, "col_"))
			if idx >= 0 && idx < 61 {
				rowSlice[idx] = fmt.Sprintf("%v", v)
			}
		}
	}

	// Logic Note & DeviceID for DataTiktok
	if isDataTiktok {
		if deviceId != "" {
			rowSlice[2] = deviceId // Col 2 DeviceID
		}
		// Note logic
		newContent := ""
		if note, ok := bodyMap["note"].(string); ok {
			newContent = note
		} else {
			// fallback lấy từ col_1
			newContent = fmt.Sprintf("%v", rowSlice[1])
		}
		
		oldNote := ""
		if !isAppend && targetIndex != -1 {
			oldNote = cacheItem.RawValues[targetIndex].Note
		}
		
		mode := "updated"
		if isAppend { mode = "new" }
		
		rowSlice[1] = utils.CreateStandardNote(oldNote, newContent, mode)
	}

	// Map back to struct
	currentAcc.FromSlice(rowSlice)
	
	// Execute Update/Append
	q := queue.GetQueue(spreadsheetId, sheetSvc)
	
	if isAppend {
		currentAcc.RowIndex = 11 + len(cacheItem.RawValues)
		cacheItem.RawValues = append(cacheItem.RawValues, currentAcc)
		cacheItem.LastAccessed = time.Now()
		
		q.EnqueueAppend(sheetName, rowSlice)
		
		p1, p2, p3 := SplitProfile(currentAcc)
		utils.JSONResponseRaw(w, map[string]interface{}{
			"status": "true", "type": "updated", "messenger": "Thêm mới thành công",
			"auth_profile": p1, "activity_profile": p2, "ai_profile": p3,
		})
	} else {
		cacheItem.RawValues[targetIndex] = currentAcc // Update pointer in Cache
		cacheItem.LastAccessed = time.Now()
		
		q.EnqueueUpdate(sheetName, currentAcc.RowIndex, rowSlice)
		
		p1, p2, p3 := SplitProfile(currentAcc)
		utils.JSONResponseRaw(w, map[string]interface{}{
			"status": "true", "type": "updated", "messenger": "Cập nhật thành công",
			"row_index": currentAcc.RowIndex,
			"auth_profile": p1, "activity_profile": p2, "ai_profile": p3,
		})
	}
}
