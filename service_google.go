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

func InitGoogleService(credJSON []byte) {
	ctx := context.Background()
	srv, err := sheets.NewService(ctx, option.WithCredentialsJSON(credJSON))
	if err != nil {
		log.Fatalf("❌ [GOOGLE INIT] Error: %v", err)
	}
	sheetsService = srv
	fmt.Println("✅ Google Service initialized (Partitioned Cache Ready).")
}

// 🔥 Hàm nạp dữ liệu thông minh (Smart Load)
func LayDuLieu(spreadsheetId, sheetName string, forceLoad bool) (*SheetCacheData, error) {
	STATE.SheetMutex.RLock()
	cacheKey := spreadsheetId + KEY_SEPARATOR + sheetName
	cached, exists := STATE.SheetCache[cacheKey]
	STATE.SheetMutex.RUnlock()

	// Nếu có Cache và chưa hết hạn -> Trả về ngay
	if exists && !forceLoad {
		if time.Now().UnixMilli()-cached.Timestamp < cached.TTL {
			// Update access time (dùng atomic hoặc lock nhẹ nếu cần, ở đây bỏ qua cho đơn giản)
			return cached, nil
		}
	}

	// Nếu không có hoặc ép load lại -> Gọi Google API
	readRange := fmt.Sprintf("'%s'!A%d:%s%d", sheetName, RANGES.DATA_START_ROW, RANGES.LIMIT_COL_FULL, RANGES.DATA_MAX_ROW)
	resp, err := sheetsService.Spreadsheets.Values.Get(spreadsheetId, readRange).Do()
	if err != nil {
		return nil, err
	}

	rawRows := resp.Values
	if rawRows == nil {
		rawRows = [][]interface{}{}
	}

	// Khởi tạo cấu trúc phân vùng
	cleanValues := make([][]string, len(rawRows))
	assignedMap := make(map[string]int)
	unassignedList := make([]int, 0)
	statusMap := make(map[string][]int)

	// 🔥 PHÂN LOẠI DỮ LIỆU (PARTITIONING)
	isDataTiktok := (sheetName == SHEET_NAMES.DATA_TIKTOK)

	for i, row := range rawRows {
		// Chuẩn hóa row thành mảng string sạch (CleanValues)
		cleanRow := make([]string, CACHE.CLEAN_COL_LIMIT)
		for j := 0; j < CACHE.CLEAN_COL_LIMIT; j++ {
			if j < len(row) {
				cleanRow[j] = CleanString(row[j])
			} else {
				cleanRow[j] = ""
			}
		}
		cleanValues[i] = cleanRow

		// Chỉ phân loại nếu là Sheet DataTiktok
		if isDataTiktok {
			deviceID := cleanRow[INDEX_DATA_TIKTOK.DEVICE_ID] // Cột C (Index 2)
			status := cleanRow[INDEX_DATA_TIKTOK.STATUS]      // Cột A (Index 0)

			// 1. Phân loại theo DeviceID (Sở hữu riêng vs Kho chung)
			if deviceID != "" {
				assignedMap[deviceID] = i // Map nhanh: DeviceID -> RowIndex
			} else {
				unassignedList = append(unassignedList, i) // List nick trống
			}

			// 2. Phân loại theo Status (Để tìm nhanh theo nhóm)
			if status != "" {
				statusMap[status] = append(statusMap[status], i)
			}
		}
	}

	// Đóng gói vào Cache
	newData := &SheetCacheData{
		RawValues:      rawRows,
		CleanValues:    cleanValues,
		AssignedMap:    assignedMap,
		UnassignedList: unassignedList,
		StatusMap:      statusMap,
		Timestamp:      time.Now().UnixMilli(),
		TTL:            CACHE.SHEET_VALID_MS,
		LastAccessed:   time.Now().UnixMilli(),
	}

	STATE.SheetMutex.Lock()
	STATE.SheetCache[cacheKey] = newData
	STATE.SheetMutex.Unlock()

	return newData, nil
}

// --- QUEUE SYSTEM (Hệ thống ghi đĩa) ---

func QueueUpdate(sid, sheetName string, rowIndex int, rowData []interface{}) {
	STATE.QueueMutex.Lock()
	defer STATE.QueueMutex.Unlock()

	if _, ok := STATE.WriteQueue[sid]; !ok {
		STATE.WriteQueue[sid] = &WriteQueueData{
			Updates: make(map[string]map[int][]interface{}),
			Appends: make(map[string][][]interface{}),
		}
	}
	q := STATE.WriteQueue[sid]

	if _, ok := q.Updates[sheetName]; !ok {
		q.Updates[sheetName] = make(map[int][]interface{})
	}
	q.Updates[sheetName][rowIndex] = rowData

	if !q.Timer {
		q.Timer = true
		go func(id string) {
			time.Sleep(time.Duration(QUEUE.FLUSH_INTERVAL_MS) * time.Millisecond)
			FlushQueue(id, false)
		}(sid)
	}
}

func QueueAppend(sid, sheetName string, rowsData [][]interface{}) {
	STATE.QueueMutex.Lock()
	defer STATE.QueueMutex.Unlock()

	if _, ok := STATE.WriteQueue[sid]; !ok {
		STATE.WriteQueue[sid] = &WriteQueueData{
			Updates: make(map[string]map[int][]interface{}),
			Appends: make(map[string][][]interface{}),
		}
	}
	q := STATE.WriteQueue[sid]

	q.Appends[sheetName] = append(q.Appends[sheetName], rowsData...)

	if !q.Timer {
		q.Timer = true
		go func(id string) {
			time.Sleep(time.Duration(QUEUE.FLUSH_INTERVAL_MS) * time.Millisecond)
			FlushQueue(id, false)
		}(sid)
	}
}

func FlushQueue(sid string, isShutdown bool) {
	STATE.QueueMutex.Lock()
	q, ok := STATE.WriteQueue[sid]
	if !ok || q.IsFlushing {
		STATE.QueueMutex.Unlock()
		return
	}
	q.IsFlushing = true
	q.Timer = false // Reset timer flag

	// Snapshot dữ liệu để nhả Lock sớm
	updates := q.Updates
	appends := q.Appends
	// Reset Queue
	q.Updates = make(map[string]map[int][]interface{})
	q.Appends = make(map[string][][]interface{})
	STATE.QueueMutex.Unlock()

	// Thực thi ghi (Không giữ Lock)
	for sheet, rowMap := range updates {
		var batchData []*sheets.ValueRange
		for idx, row := range rowMap {
			rng := fmt.Sprintf("'%s'!A%d", sheet, RANGES.DATA_START_ROW+idx)
			batchData = append(batchData, &sheets.ValueRange{
				Range:  rng,
				Values: [][]interface{}{row},
			})
		}
		if len(batchData) > 0 {
			_, err := sheetsService.Spreadsheets.Values.BatchUpdate(sid, &sheets.BatchUpdateValuesRequest{
				ValueInputOption: "RAW",
				Data:             batchData,
			}).Do()
			if err != nil {
				log.Printf("❌ [FLUSH UPDATE] %s: %v", sheet, err)
			}
		}
	}

	for sheet, rows := range appends {
		if len(rows) > 0 {
			_, err := sheetsService.Spreadsheets.Values.Append(sid, fmt.Sprintf("'%s'!A1", sheet), &sheets.ValueRange{
				Values: rows,
			}).ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Do()
			if err != nil {
				log.Printf("❌ [FLUSH APPEND] %s: %v", sheet, err)
			}
		}
	}

	STATE.QueueMutex.Lock()
	q.IsFlushing = false
	// Nếu trong lúc ghi có dữ liệu mới -> Kích hoạt timer tiếp
	if (len(q.Updates) > 0 || len(q.Appends) > 0) && !q.Timer && !isShutdown {
		q.Timer = true
		go func(id string) {
			time.Sleep(time.Duration(QUEUE.FLUSH_INTERVAL_MS) * time.Millisecond)
			FlushQueue(id, false)
		}(sid)
	}
	STATE.QueueMutex.Unlock()
}
