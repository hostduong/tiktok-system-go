package queue

import (
	"fmt"
	"log"
	"sync"
	"time"

	"tiktok-server/internal/models"
	"tiktok-server/internal/sheets"
)

// QueueManager quản lý hàng đợi ghi cho từng Spreadsheet
type QueueManager struct {
	sync.Mutex // Khóa bảo vệ hàng đợi

	SpreadsheetID string
	SheetSvc      *sheets.Service
	
	// Lưu các dòng cần Update: Map[SheetName][RowIndex] -> Data
	Updates map[string]map[int]*models.TikTokAccount
	
	// Lưu các dòng cần Append (Log): Map[SheetName] -> List of Rows
	Appends map[string][][]interface{}

	IsFlushing bool
	Timer      *time.Timer
}

// GlobalQueues: Quản lý nhiều file Sheet khác nhau
var (
	GlobalQueues = sync.Map{} // Map[SpreadsheetID]*QueueManager
)

// GetQueue: Lấy (hoặc tạo) Queue cho 1 file Sheet
func GetQueue(sid string, svc *sheets.Service) *QueueManager {
	if val, ok := GlobalQueues.Load(sid); ok {
		return val.(*QueueManager)
	}

	q := &QueueManager{
		SpreadsheetID: sid,
		SheetSvc:      svc,
		Updates:       make(map[string]map[int]*models.TikTokAccount),
		Appends:       make(map[string][][]interface{}),
	}
	GlobalQueues.Store(sid, q)
	return q
}

// EnqueueUpdate: Đẩy lệnh update vào hàng đợi
func (q *QueueManager) EnqueueUpdate(sheetName string, rowIndex int, data *models.TikTokAccount) {
	q.Lock()
	defer q.Unlock()

	if _, ok := q.Updates[sheetName]; !ok {
		q.Updates[sheetName] = make(map[int]*models.TikTokAccount)
	}
	// Cơ chế đè: Lệnh mới nhất sẽ thắng
	q.Updates[sheetName][rowIndex] = data

	q.checkTrigger()
}

// EnqueueAppend: Đẩy lệnh thêm mới (Log)
func (q *QueueManager) EnqueueAppend(sheetName string, rowData []interface{}) {
	q.Lock()
	defer q.Unlock()

	q.Appends[sheetName] = append(q.Appends[sheetName], rowData)
	q.checkTrigger()
}

// Smart Piggyback Logic: Kiểm tra xem có cần xả hàng ngay không
func (q *QueueManager) checkTrigger() {
	total := 0
	for _, m := range q.Updates { total += len(m) }
	for _, l := range q.Appends { total += len(l) }

	// Nếu > 100 dòng -> Ép xả ngay (Giống Node.js)
	if total > 100 {
		if q.Timer != nil { q.Timer.Stop() }
		go q.Flush(false)
		return
	}

	// Nếu chưa có timer -> Hẹn giờ 3 giây
	if q.Timer == nil {
		q.Timer = time.AfterFunc(3*time.Second, func() {
			q.Flush(false)
		})
	}
}

// Flush: Ghi dữ liệu xuống Google Sheets
func (q *QueueManager) Flush(isShutdown bool) {
	q.Lock()
	if q.IsFlushing {
		q.Unlock()
		return
	}
	q.IsFlushing = true
	
	// Snapshot: Lấy dữ liệu ra để xử lý, giải phóng hàng đợi
	pendingUpdates := q.Updates
	pendingAppends := q.Appends
	
	// Reset Queue
	q.Updates = make(map[string]map[int]*models.TikTokAccount)
	q.Appends = make(map[string][][]interface{})
	q.Timer = nil
	q.Unlock()

	defer func() {
		q.Lock()
		q.IsFlushing = false
		q.Unlock()
	}()

	// 1. Xử lý Update (Batch Update)
	for sheetName, rowsMap := range pendingUpdates {
		if len(rowsMap) == 0 { continue }
		err := q.SheetSvc.BatchUpdateRows(q.SpreadsheetID, sheetName, rowsMap)
		if err != nil {
			log.Printf("❌ [FLUSH UPDATE ERROR] %s: %v", sheetName, err)
		} else {
			log.Printf("✅ [FLUSH] Đã cập nhật %d dòng vào %s", len(rowsMap), sheetName)
		}
	}

	// 2. Xử lý Append (Log)
	for sheetName, rowsList := range pendingAppends {
		if len(rowsList) == 0 { continue }
		// Dùng hàm AppendRawRows (đã thêm vào sheets/client.go)
		err := q.SheetSvc.AppendRawRows(q.SpreadsheetID, sheetName, rowsList)
		if err != nil {
			log.Printf("❌ [FLUSH APPEND ERROR] %s: %v", sheetName, err)
		} else {
			log.Printf("✅ [FLUSH] Đã thêm %d dòng vào %s", len(rowsList), sheetName)
		}
	}
}
