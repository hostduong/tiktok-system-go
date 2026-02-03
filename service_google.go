package main

import (
	"context"
	"fmt"
	"log"
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

// --- QUEUE SYSTEM (Hệ thống ghi đĩa - FIX BUG DEADLOCK) ---

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
	// Nếu Queue không tồn tại hoặc ĐANG CÓ NGƯỜI GHI -> Thoát ngay
	if !ok || q.IsFlushing {
		STATE.QueueMutex.Unlock()
		return
	}
	
	// Đánh dấu đang ghi
	q.IsFlushing = true
	q.Timer = false // Reset Timer để các request mới có thể hẹn giờ tiếp
	
	// Snapshot dữ liệu để nhả Lock sớm
	updates := q.Updates
	appends := q.Appends
	// Reset Queue (Tạo map mới để hứng dữ liệu mới trong lúc đang ghi cũ)
	q.Updates = make(map[string]map[int][]interface{})
	q.Appends = make(map[string][][]interface{})
	STATE.QueueMutex.Unlock()

	// --- BẮT ĐẦU GHI (KHÔNG GIỮ LOCK) ---

	// 1. Ghi Update (BatchUpdate)
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

	// 2. Ghi Append
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

	// --- KẾT THÚC GHI & KIỂM TRA LẠI ---
	STATE.QueueMutex.Lock()
	q.IsFlushing = false
	
	// 🔥 FIX BUG DEADLOCK: 
	// Luôn kiểm tra lại xem có dữ liệu mới ập đến trong lúc đang ghi không.
	// BỎ QUA kiểm tra !q.Timer, cứ có dữ liệu là phải đảm bảo có Timer chạy.
	if (len(q.Updates) > 0 || len(q.Appends) > 0) && !isShutdown {
		// Luôn reset timer và chạy lại để đảm bảo không bỏ sót
		q.Timer = true 
		go func(id string) {
			time.Sleep(time.Duration(QUEUE.FLUSH_INTERVAL_MS) * time.Millisecond)
			FlushQueue(id, false)
		}(sid)
	}
	STATE.QueueMutex.Unlock()
}
