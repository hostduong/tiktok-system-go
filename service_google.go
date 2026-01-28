package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

var sheetsService *sheets.Service

// InitGoogleService: Sử dụng quyền ADC của Cloud Run (Không dùng JSON Key)
func InitGoogleService(credJSON []byte) {
	ctx := context.Background()
	
	// Kết nối bằng quyền của Cloud Run (Gmail A)
	srv, err := sheets.NewService(ctx, 
		option.WithScopes(
			"https://www.googleapis.com/auth/spreadsheets",
			"https://www.googleapis.com/auth/drive",
		),
	)
	
	if err != nil {
		log.Printf("❌ [CRITICAL] Google Sheets Init Error: %v", err)
		sheetsService = nil
		return
	}
	
	sheetsService = srv
	fmt.Println("✅ Google Service initialized (ADC - Cloud Run Identity).")
}

// =================================================================================================
// 🟢 CORE LOGIC
// =================================================================================================

func LayDuLieu(spreadsheetId string, sheetName string, forceLoad bool) (*SheetCacheData, error) {
	if sheetsService == nil { 
		return nil, fmt.Errorf("Google Sheets Service chưa kết nối") 
	}

	if sheetName == "" { sheetName = SHEET_NAMES.DATA_TIKTOK }

	// 1. Check RAM
	cacheKey := spreadsheetId + KEY_SEPARATOR + sheetName
	now := time.Now().UnixMilli()

	STATE.SheetMutex.RLock()
	cache, exists := STATE.SheetCache[cacheKey]
	STATE.SheetMutex.RUnlock()

	hasPendingWrite := CheckPendingWrite(spreadsheetId, sheetName)
	
	// Sử dụng config
	if !forceLoad && exists && ((now-cache.Timestamp < CACHE.SHEET_VALID_MS) || hasPendingWrite) {
		STATE.SheetMutex.Lock()
		cache.LastAccessed = now
		STATE.SheetMutex.Unlock()
		return cache, nil
	}

	// 2. Load from Google
	readRange := fmt.Sprintf("'%s'!A%d:%s%d", sheetName, RANGES.DATA_START_ROW, RANGES.LIMIT_COL_FULL, RANGES.DATA_MAX_ROW)
	
	resp, err := CallGoogleAPI(func() (interface{}, error) {
		return sheetsService.Spreadsheets.Values.Get(spreadsheetId, readRange).ValueRenderOption("UNFORMATTED_VALUE").Do()
	})
	
	if err != nil {
		fmt.Printf("❌ [GOOGLE API ERROR] SID: %s | Range: %s | Error: %v\n", spreadsheetId, readRange, err)
		return nil, err
	}
	
	valuesResp, ok := resp.(*sheets.ValueRange)
	if !ok { return nil, fmt.Errorf("invalid response type") }

	rawRows := valuesResp.Values
	
	// 3. Normalize Data
	normalizedRawValues := make([][]interface{}, 0)
	cleanValues := make([][]string, 0)
	indices := make(map[string]map[string]int)
	indices["userId"] = make(map[string]int)
	indices["userSec"] = make(map[string]int)
	indices["userName"] = make(map[string]int)
	indices["email"] = make(map[string]int)
	statusIndices := make(map[string][]int)

	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	for i, row := range rawRows {
		// Đảm bảo đủ 61 cột
		fullRow := make([]interface{}, 61)
		for j, cell := range row { if j < 61 { fullRow[j] = cell } }
		
		// 🔥 FIX PANIC: Luôn tạo mảng sạch đủ 61 phần tử
		// (Thay vì chỉ lấy 7 phần tử như config cũ, vì logic cần đọc password ở cột 8)
		shortClean := make([]string, 61)
		for k := 0; k < 61; k++ { 
			shortClean[k] = CleanString(fullRow[k]) 
		}

		normalizedRawValues = append(normalizedRawValues, fullRow)
		cleanValues = append(cleanValues, shortClean)

		if isDataTiktok {
			uid := shortClean[INDEX_DATA_TIKTOK.USER_ID]
			sec := shortClean[INDEX_DATA_TIKTOK.USER_SEC]
			uName := shortClean[INDEX_DATA_TIKTOK.USER_NAME]
			email := shortClean[INDEX_DATA_TIKTOK.EMAIL]
			status := shortClean[INDEX_DATA_TIKTOK.STATUS]

			if uid != "" { indices["userId"][uid] = i }
			if sec != "" { indices["userSec"][sec] = i }
			if uName != "" { indices["userName"][uName] = i }
			if strings.Contains(email, "@") { indices["email"][email] = i }
			if status != "" { statusIndices[status] = append(statusIndices[status], i) }
		}
	}

	newCache := &SheetCacheData{
		RawValues: normalizedRawValues, CleanValues: cleanValues,
		Indices: indices, StatusIndices: statusIndices,
		Timestamp: now, TTL: CACHE.SHEET_VALID_MS, LastAccessed: now, Source: "sheet",
	}

	STATE.SheetMutex.Lock()
	STATE.SheetCache[cacheKey] = newCache
	STATE.SheetMutex.Unlock()

	return newCache, nil
}

func CallGoogleAPI(fn func() (interface{}, error)) (interface{}, error) {
	retries := 3
	for i := 0; i < retries; i++ {
		res, err := fn()
		if err == nil { return res, nil }
		errStr := err.Error()
		if strings.Contains(errStr, "400") || strings.Contains(errStr, "403") || strings.Contains(errStr, "404") || strings.Contains(errStr, "invalid") {
			return nil, err
		}
		time.Sleep(time.Duration(1<<i) * time.Second)
	}
	return nil, fmt.Errorf("Max retries exceeded")
}

// --- QUEUE FUNCTIONS ---
func QueueUpdate(sid string, sheetName string, rowIndex int, data []interface{}) {
	q := GetQueue(sid)
	q.Mutex.Lock()
	defer q.Mutex.Unlock()
	if q.Updates[sheetName] == nil { q.Updates[sheetName] = make(map[int][]interface{}) }
	q.Updates[sheetName][rowIndex] = data
	if q.Timer == nil {
		q.Timer = time.AfterFunc(time.Duration(QUEUE.FLUSH_INTERVAL_MS)*time.Millisecond, func() { FlushQueue(sid, false) })
	}
}

func QueueAppend(sid string, sheetName string, rows [][]interface{}) {
	q := GetQueue(sid)
	q.Mutex.Lock()
	defer q.Mutex.Unlock()
	if q.Appends[sheetName] == nil { q.Appends[sheetName] = make([][]interface{}, 0) }
	q.Appends[sheetName] = append(q.Appends[sheetName], rows...)
	if q.Timer == nil {
		q.Timer = time.AfterFunc(time.Duration(QUEUE.FLUSH_INTERVAL_MS)*time.Millisecond, func() { FlushQueue(sid, false) })
	}
}

func FlushQueue(sid string, isShutdown bool) {
	q := GetQueue(sid)
	q.Mutex.Lock()
	if q.IsFlushing { q.Mutex.Unlock(); return }
	q.IsFlushing = true
	if !isShutdown && q.Timer != nil { q.Timer.Stop(); q.Timer = nil }
	
	updatesSnapshot := make(map[string]map[int][]interface{})
	appendsSnapshot := make(map[string][][]interface{})
	for s, m := range q.Updates {
		updatesSnapshot[s] = make(map[int][]interface{})
		for r, d := range m { updatesSnapshot[s][r] = d }
		delete(q.Updates, s)
	}
	for s, arr := range q.Appends {
		appendsSnapshot[s] = arr
		delete(q.Appends, s)
	}
	q.Mutex.Unlock()

	defer func() {
		q.Mutex.Lock()
		q.IsFlushing = false
		if (len(q.Updates) > 0 || len(q.Appends) > 0) && !isShutdown && q.Timer == nil {
			q.Timer = time.AfterFunc(time.Duration(QUEUE.FLUSH_INTERVAL_MS)*time.Millisecond, func() { FlushQueue(sid, false) })
		}
		q.Mutex.Unlock()
	}()

	if sheetsService == nil { return }

	valueUpdates := []*sheets.ValueRange{}
	for sheetName, rowsMap := range updatesSnapshot {
		for rIdx, data := range rowsMap {
			actualRow := RANGES.DATA_START_ROW + rIdx
			rng := fmt.Sprintf("'%s'!A%d:%s%d", sheetName, actualRow, RANGES.LIMIT_COL_FULL, actualRow)
			valueUpdates = append(valueUpdates, &sheets.ValueRange{ Range: rng, Values: [][]interface{}{data} })
		}
	}

	if len(valueUpdates) > 0 {
		CallGoogleAPI(func() (interface{}, error) {
			return sheetsService.Spreadsheets.Values.BatchUpdate(sid, &sheets.BatchUpdateValuesRequest{ ValueInputOption: "RAW", Data: valueUpdates }).Do()
		})
	}

	for sheetName, rows := range appendsSnapshot {
		if len(rows) == 0 { continue }
		rng := fmt.Sprintf("'%s'!A1", sheetName)
		CallGoogleAPI(func() (interface{}, error) {
			return sheetsService.Spreadsheets.Values.Append(sid, rng, &sheets.ValueRange{ Values: rows }).ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Do()
		})
	}
}

func QueueMailUpdate(sid string, rowIndex int) {
	STATE.MailMutex.Lock()
	defer STATE.MailMutex.Unlock()
	q := STATE.MailQueue[sid]
	if q == nil { q = &MailQueueData{Rows: make(map[int]bool)}; STATE.MailQueue[sid] = q }
	q.Rows[rowIndex] = true
	if q.Timer == nil {
		q.Timer = time.AfterFunc(time.Duration(QUEUE.FLUSH_INTERVAL_MS)*time.Millisecond, func() { FlushMailQueue(sid, false) })
	}
}

func FlushMailQueue(sid string, isShutdown bool) {
	STATE.MailMutex.Lock()
	q := STATE.MailQueue[sid]
	if q == nil || q.IsFlushing { STATE.MailMutex.Unlock(); return }
	q.IsFlushing = true
	if !isShutdown && q.Timer != nil { q.Timer.Stop(); q.Timer = nil }
	rowsToFlush := make([]int, 0, len(q.Rows))
	for r := range q.Rows { rowsToFlush = append(rowsToFlush, r) }
	for _, r := range rowsToFlush { delete(q.Rows, r) }
	STATE.MailMutex.Unlock()

	defer func() {
		STATE.MailMutex.Lock()
		q.IsFlushing = false
		if len(q.Rows) > 0 && !isShutdown && q.Timer == nil {
			q.Timer = time.AfterFunc(time.Duration(QUEUE.FLUSH_INTERVAL_MS)*time.Millisecond, func() { FlushMailQueue(sid, false) })
		}
		STATE.MailMutex.Unlock()
	}()

	if len(rowsToFlush) == 0 || sheetsService == nil { return }
	batchRequests := []*sheets.ValueRange{}
	for _, rIdx := range rowsToFlush {
		rng := fmt.Sprintf("'%s'!H%d", SHEET_NAMES.EMAIL_LOGGER, rIdx)
		batchRequests = append(batchRequests, &sheets.ValueRange{ Range: rng, Values: [][]interface{}{{"TRUE"}} })
	}
	CallGoogleAPI(func() (interface{}, error) {
		return sheetsService.Spreadsheets.Values.BatchUpdate(sid, &sheets.BatchUpdateValuesRequest{ ValueInputOption: "RAW", Data: batchRequests }).Do()
	})
}

func CleanupEmail(sid string) {} 

func GetQueue(sid string) *WriteQueueData {
	STATE.QueueMutex.Lock()
	defer STATE.QueueMutex.Unlock()
	if STATE.WriteQueue[sid] == nil {
		STATE.WriteQueue[sid] = &WriteQueueData{ Updates: make(map[string]map[int][]interface{}), Appends: make(map[string][][]interface{}), SheetRetries: make(map[string]int) }
	}
	return STATE.WriteQueue[sid]
}

func CheckPendingWrite(sid string, sheetName string) bool {
	STATE.QueueMutex.RLock()
	defer STATE.QueueMutex.RUnlock()
	q, ok := STATE.WriteQueue[sid]
	if !ok { return false }
	if _, hasUp := q.Updates[sheetName]; hasUp && len(q.Updates[sheetName]) > 0 { return true }
	if _, hasAp := q.Appends[sheetName]; hasAp && len(q.Appends[sheetName]) > 0 { return true }
	return false
}
