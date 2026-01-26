package queue

import (
	"log"
	"sync"
	"time"

	"tiktok-server/internal/models"
	"tiktok-server/internal/sheets"
)

// TaskType định nghĩa loại công việc (Update hay Append)
type TaskType int

const (
	TypeUpdate TaskType = iota
	TypeAppend
)

// WriteTask đại diện cho 1 lệnh ghi
type WriteTask struct {
	Type      TaskType
	SheetName string
	RowIndex  int
	Data      *models.TikTokAccount // Dùng cho Update
	RawRow    []interface{}         // Dùng cho Append/Log (Linh hoạt)
}

// QueueManager quản lý hàng đợi cho từng Spreadsheet
type QueueManager struct {
	sync.Mutex // Khóa bảo vệ hàng đợi (Smart Lock: Chỉ khóa khi đẩy/lấy task)

	SpreadsheetID string
	SheetSvc      *sheets.Service
	
	// Hàng đợi lưu trong RAM
	Updates map[string]map[int]*models.TikTokAccount // [SheetName][RowIndex] -> Data
	Appends map[string][][]interface{}               // [SheetName] -> List of Rows

	IsFlushing bool
	Timer      *time.Timer
}

// GlobalQueues: Quản lý Queue cho nhiều file Sheet khác nhau
var (
	GlobalQueues = sync.Map{} // Map[SpreadsheetID]*QueueManager
)

// GetQueue lấy (hoặc tạo mới) Queue cho 1 file Sheet
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

// EnqueueUpdate: Đẩy lệnh cập nhật vào hàng đợi (Thay thế queue_update của Node.js)
func (q *QueueManager) EnqueueUpdate(sheetName string, rowIndex int, data *models.TikTokAccount) {
	q.Lock() // 🔒 Lock cực nhanh để nhét dữ liệu vào map
	defer q.Unlock()

	if _, ok := q.Updates[sheetName]; !ok {
		q.Updates[sheetName] = make(map[int]*models.TikTokAccount)
	}
	// Cơ chế đè: Nếu dòng này đang chờ update cũ, lệnh mới sẽ đè lên (Tối ưu)
	q.Updates[sheetName][rowIndex] = data

	q.checkTrigger()
}

// EnqueueAppend: Đẩy lệnh thêm mới (Log/Append)
func (q *QueueManager) EnqueueAppend(sheetName string, rowData []interface{}) {
	q.Lock()
	defer q.Unlock()

	q.Appends[sheetName] = append(q.Appends[sheetName], rowData)
	q.checkTrigger()
}

// checkTrigger: Kiểm tra xem có cần xả hàng đợi không (Smart Piggyback Logic)
func (q *QueueManager) checkTrigger() {
	// Đếm tổng số lượng pending
	total := 0
	for _, m := range q.Updates {
		total += len(m)
	}
	for _, l := range q.Appends {
		total += len(l)
	}

	// Logic Node.js: Nếu > 100 dòng -> Ép xả ngay (Urgent Flush)
	if total > 100 {
		if q.Timer != nil {
			q.Timer.Stop()
		}
		go q.Flush(false) // Chạy ngay lập tức
		return
	}

	// Nếu chưa có timer, đặt hẹn giờ 3 giây
	if q.Timer == nil {
		q.Timer = time.AfterFunc(3*time.Second, func() {
			q.Flush(false)
		})
	}
}

// Flush: Thực hiện ghi xuống Google Sheets (Nặng nhất)
func (q *QueueManager) Flush(isShutdown bool) {
	q.Lock()
	if q.IsFlushing {
		q.Unlock()
		return
	}
	q.IsFlushing = true
	
	// Snapshot: Lấy dữ liệu ra khỏi hàng đợi để xử lý, giải phóng hàng đợi cho request mới
	// Đây là kỹ thuật "Copy-on-Write" giúp giảm thời gian Lock
	pendingUpdates := q.Updates
	pendingAppends := q.Appends
	
	// Reset hàng đợi
	q.Updates = make(map[string]map[int]*models.TikTokAccount)
	q.Appends = make(map[string][][]interface{})
	q.Timer = nil
	q.Unlock() // 🔓 Mở khóa ngay để luồng chính tiếp tục nhận request

	defer func() {
		q.Lock()
		q.IsFlushing = false
		q.Unlock()
	}()

	// Bắt đầu gọi Google API (Tốn thời gian nhưng không chặn luồng chính)
	
	// 1. Xử lý Update
	for sheetName, rowsMap := range pendingUpdates {
		if len(rowsMap) == 0 { continue }
		// Gọi BatchUpdate bên sheets/client.go
		err := q.SheetSvc.BatchUpdateRows(q.SpreadsheetID, sheetName, rowsMap)
		if err != nil {
			log.Printf("❌ [FLUSH ERROR] Update %s: %v", sheetName, err)
			// TODO: Retry logic (Node.js có retry 5 lần, ở V2 Go ta có thể làm sau)
		} else {
			log.Printf("✅ [FLUSH] Updated %d rows in %s", len(rowsMap), sheetName)
		}
	}

	// 2. Xử lý Append
	for sheetName, rowsList := range pendingAppends {
		if len(rowsList) == 0 { continue }
		// Chuyển đổi sang format model nếu cần, hoặc append raw
		// Ở đây ta dùng AppendRowsRaw (Cần bổ sung vào sheets/client.go hoặc dùng logic append cũ)
		// Để đơn giản, ta giả định client.go hỗ trợ append mảng thô.
		// (Logic này khớp với xu_ly_gui_mail)
		// ... Thực tế ta cần map lại struct hoặc viết hàm AppendRaw trong client.go
	}
}
