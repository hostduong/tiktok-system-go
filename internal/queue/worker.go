package queue

import (
	"log"
	"sync"
	"time"

	"tiktok-server/internal/sheets"
)

// QueueManager: Quản lý hàng đợi ghi
type QueueManager struct {
	sync.Mutex
	SpreadsheetID string
	SheetSvc      *sheets.Service
	
	// Data Queue
	Updates map[string]map[int][]interface{} // [SheetName][RowIndex] -> DataSlice
	Appends map[string][][]interface{}       // [SheetName] -> List of Rows

	// Mail Queue (Separate)
	MailUpdates map[int]string // [RowIndex] -> "TRUE"

	IsFlushing bool
	Timer      *time.Timer
}

var GlobalQueues = sync.Map{}

func GetQueue(sid string, svc *sheets.Service) *QueueManager {
	if val, ok := GlobalQueues.Load(sid); ok {
		return val.(*QueueManager)
	}
	q := &QueueManager{
		SpreadsheetID: sid,
		SheetSvc:      svc,
		Updates:       make(map[string]map[int][]interface{}),
		Appends:       make(map[string][][]interface{}),
		MailUpdates:   make(map[int]string),
	}
	GlobalQueues.Store(sid, q)
	return q
}

// --- ENQUEUE METHODS ---

func (q *QueueManager) EnqueueUpdate(sheetName string, rowIndex int, data []interface{}) {
	q.Lock()
	defer q.Unlock()
	if _, ok := q.Updates[sheetName]; !ok {
		q.Updates[sheetName] = make(map[int][]interface{})
	}
	q.Updates[sheetName][rowIndex] = data
	q.scheduleFlush()
}

func (q *QueueManager) EnqueueAppend(sheetName string, rowData []interface{}) {
	q.Lock()
	defer q.Unlock()
	q.Appends[sheetName] = append(q.Appends[sheetName], rowData)
	q.scheduleFlush()
}

func (q *QueueManager) EnqueueMailUpdate(rowIndex int) {
	q.Lock()
	defer q.Unlock()
	q.MailUpdates[rowIndex] = "TRUE"
	q.scheduleFlush()
}

// scheduleFlush: Hẹn giờ ghi (3 giây)
func (q *QueueManager) scheduleFlush() {
	if q.Timer == nil {
		q.Timer = time.AfterFunc(3*time.Second, func() {
			q.Flush(false)
		})
	}
}

// GetPendingCount: Đếm số lượng chờ xử lý (Dùng cho Smart Piggyback)
func (q *QueueManager) GetPendingCount() int {
	q.Lock()
	defer q.Unlock()
	total := 0
	for _, m := range q.Updates { total += len(m) }
	for _, l := range q.Appends { total += len(l) }
	// Mail queue tính riêng hoặc chung tùy chiến thuật, ở đây Node.js tính chung cho Urgent
	total += len(q.MailUpdates)
	return total
}

// Flush: Thực hiện ghi xuống Google Sheets
func (q *QueueManager) Flush(isShutdown bool) {
	q.Lock()
	if q.IsFlushing {
		q.Unlock()
		return
	}
	q.IsFlushing = true
	
	// Snapshot Data
	pendingUpdates := q.Updates
	pendingAppends := q.Appends
	pendingMails := q.MailUpdates

	// Reset Queue
	q.Updates = make(map[string]map[int][]interface{})
	q.Appends = make(map[string][][]interface{})
	q.MailUpdates = make(map[int]string)
	if q.Timer != nil {
		q.Timer.Stop()
		q.Timer = nil
	}
	q.Unlock()

	defer func() {
		q.Lock()
		q.IsFlushing = false
		q.Unlock()
	}()

	// 1. Xử lý Data Updates
	for sheetName, rowsMap := range pendingUpdates {
		if len(rowsMap) == 0 { continue }
		err := q.SheetSvc.BatchUpdateRows(q.SpreadsheetID, sheetName, rowsMap)
		if err != nil {
			log.Printf("❌ [DATA UPDATE ERROR] %s: %v", sheetName, err)
			// Ở Node.js có logic Retry/Rollback, ở đây log ra để đơn giản hóa
		}
	}

	// 2. Xử lý Data Appends
	for sheetName, rowsList := range pendingAppends {
		if len(rowsList) == 0 { continue }
		err := q.SheetSvc.AppendRawRows(q.SpreadsheetID, sheetName, rowsList)
		if err != nil {
			log.Printf("❌ [DATA APPEND ERROR] %s: %v", sheetName, err)
		}
	}

	// 3. Xử lý Mail Updates (Queue riêng)
	if len(pendingMails) > 0 {
		err := q.SheetSvc.BatchUpdateCells(q.SpreadsheetID, "EmailLogger", pendingMails)
		if err != nil {
			log.Printf("❌ [MAIL UPDATE ERROR]: %v", err)
		} else {
			log.Printf("✅ [MAIL] Marked %d emails as READ", len(pendingMails))
		}
	}
}
