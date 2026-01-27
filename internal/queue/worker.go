package queue

import (
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
	
	// --- DATA QUEUE (Cho DataTiktok, PostLogger...) ---
	// Lưu các dòng cần Update: Map[SheetName][RowIndex] -> Data
	Updates map[string]map[int]*models.TikTokAccount
	// Lưu các dòng cần Append (Log): Map[SheetName] -> List of Rows
	Appends map[string][][]interface{}

	// --- MAIL QUEUE (Cho EmailLogger) ---
	// Map[RowIndex] -> "TRUE"
	MailUpdates map[int]string

	IsFlushing bool
	Timer      *time.Timer
}

// GlobalQueues: Quản lý Queue cho nhiều file Sheet khác nhau
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
		MailUpdates:   make(map[int]string), // Khởi tạo Mail Queue riêng
	}
	GlobalQueues.Store(sid, q)
	return q
}

// EnqueueUpdate: Đẩy lệnh cập nhật Data vào hàng đợi
func (q *QueueManager) EnqueueUpdate(sheetName string, rowIndex int, data *models.TikTokAccount) {
	q.Lock()
	defer q.Unlock()

	if _, ok := q.Updates[sheetName]; !ok {
		q.Updates[sheetName] = make(map[int]*models.TikTokAccount)
	}
	q.Updates[sheetName][rowIndex] = data

	q.checkTrigger()
}

// EnqueueAppend: Đẩy lệnh thêm mới Data vào hàng đợi
func (q *QueueManager) EnqueueAppend(sheetName string, rowData []interface{}) {
	q.Lock()
	defer q.Unlock()

	q.Appends[sheetName] = append(q.Appends[sheetName], rowData)
	q.checkTrigger()
}

// EnqueueMailUpdate: Đẩy lệnh đánh dấu Mail vào hàng đợi RIÊNG
func (q *QueueManager) EnqueueMailUpdate(rowIndex int) {
	q.Lock()
	defer q.Unlock()

	q.MailUpdates[rowIndex] = "TRUE"
	
	q.checkTrigger()
}

// checkTrigger: Smart Piggyback
func (q *QueueManager) checkTrigger() {
	total := 0
	
	for _, m := range q.Updates { total += len(m) }
	for _, l := range q.Appends { total += len(l) }
	total += len(q.MailUpdates)

	if total > 100 {
		if q.Timer != nil { q.Timer.Stop() }
		go q.Flush(false)
		return
	}

	if q.Timer == nil {
		q.Timer = time.AfterFunc(3*time.Second, func() {
			q.Flush(false)
		})
	}
}

// Flush: Thực hiện ghi xuống Google Sheets
func (q *QueueManager) Flush(isShutdown bool) {
	q.Lock()
	if q.IsFlushing {
		q.Unlock()
		return
	}
	q.IsFlushing = true
	
	pendingUpdates := q.Updates
	pendingAppends := q.Appends
	pendingMails := q.MailUpdates

	q.Updates = make(map[string]map[int]*models.TikTokAccount)
	q.Appends = make(map[string][][]interface{})
	q.MailUpdates = make(map[int]string)
	q.Timer = nil
	q.Unlock()

	defer func() {
		q.Lock()
		q.IsFlushing = false
		q.Unlock()
	}()

	// --- PHẦN 1: XỬ LÝ DATA QUEUE ---
	for sheetName, rowsMap := range pendingUpdates {
		if len(rowsMap) == 0 { continue }
		err := q.SheetSvc.BatchUpdateRows(q.SpreadsheetID, sheetName, rowsMap)
		if err != nil {
			log.Printf("❌ [FLUSH UPDATE ERROR] %s: %v", sheetName, err)
		} else {
			log.Printf("✅ [FLUSH DATA] Updated %d rows in %s", len(rowsMap), sheetName)
		}
	}

	for sheetName, rowsList := range pendingAppends {
		if len(rowsList) == 0 { continue }
		err := q.SheetSvc.AppendRawRows(q.SpreadsheetID, sheetName, rowsList)
		if err != nil {
			log.Printf("❌ [FLUSH APPEND ERROR] %s: %v", sheetName, err)
		} else {
			log.Printf("✅ [FLUSH DATA] Appended %d rows in %s", len(rowsList), sheetName)
		}
	}

	// --- PHẦN 2: XỬ LÝ MAIL QUEUE ---
	if len(pendingMails) > 0 {
		err := q.SheetSvc.BatchUpdateCells(q.SpreadsheetID, "EmailLogger", pendingMails)
		if err != nil {
			log.Printf("❌ [FLUSH MAIL ERROR]: %v", err)
		} else {
			log.Printf("✅ [FLUSH MAIL] Marked %d emails as READ", len(pendingMails))
		}
	}
}
