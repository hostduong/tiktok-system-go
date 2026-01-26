package cache

import (
	"sync"
	"time"

	"github.com/hostduong/tiktok-system-go/internal/models"
)

// SheetCacheItem lưu trữ dữ liệu của 1 file Excel trong RAM
type SheetCacheItem struct {
	sync.RWMutex // Khóa an toàn (Read-Write Mutex)

	SpreadsheetID string
	SheetName     string
	Timestamp     time.Time // Thời điểm tải dữ liệu
	TTL           time.Duration
	LastAccessed  time.Time

	// Dữ liệu chính: Danh sách các dòng
	RawValues []*models.TikTokAccount

	// Index (Chỉ mục) để tìm kiếm siêu tốc
	// Map[GiaTri] -> DanhSachViTriRow
	IndexUserID   map[string]int
	IndexEmail    map[string]int
	IndexStatus   map[string][]int
	IndexDeviceId map[string][]int // Index DeviceID để check trùng nhanh
}

// GlobalCache là kho chứa toàn bộ các Sheet đang load
var (
	GlobalSheets = sync.Map{} // Map[CacheKey]*SheetCacheItem
)

// NewSheetCache tạo một cache mới cho 1 sheet
func NewSheetCache(sid, name string) *SheetCacheItem {
	return &SheetCacheItem{
		SpreadsheetID: sid,
		SheetName:     name,
		Timestamp:     time.Now(),
		TTL:           5 * time.Minute, // Mặc định sống 5 phút như Node.js
		RawValues:     make([]*models.TikTokAccount, 0),
		IndexUserID:   make(map[string]int),
		IndexEmail:    make(map[string]int),
		IndexStatus:   make(map[string][]int),
		IndexDeviceId: make(map[string][]int),
	}
}

// IsValid kiểm tra cache còn hạn dùng không
func (s *SheetCacheItem) IsValid() bool {
	s.RLock()
	defer s.RUnlock()
	return time.Since(s.Timestamp) < s.TTL
}

// GetAccountByIndex lấy dữ liệu dòng cụ thể (An toàn luồng)
func (s *SheetCacheItem) GetAccountByIndex(idx int) *models.TikTokAccount {
	s.RLock()
	defer s.RUnlock()
	if idx < 0 || idx >= len(s.RawValues) {
		return nil
	}
	// Trả về bản sao hoặc con trỏ? Ở đây trả về con trỏ để tiết kiệm RAM
	return s.RawValues[idx]
}

// UpdateAccount cập nhật dữ liệu vào RAM (An toàn luồng)
func (s *SheetCacheItem) UpdateAccount(idx int, newData *models.TikTokAccount) {
	s.Lock() // Khóa ghi (Chặn tất cả các luồng khác đọc/ghi lúc này)
	defer s.Unlock()

	if idx < 0 || idx >= len(s.RawValues) {
		return
	}

	// Cập nhật dữ liệu chính
	s.RawValues[idx] = newData
	s.LastAccessed = time.Now()

	// TODO: Logic cập nhật lại Index (sẽ viết kỹ hơn ở phần Logic)
	// Vì khi đổi Status, ta phải xóa ở Index cũ và thêm vào Index mới
}

// OptimisticLockingCheck kiểm tra và khóa nick (Core logic của V243)
// Trả về: (Thành công hay không, RowIndex)
func (s *SheetCacheItem) OptimisticLockingCheck(reqDevice string, potentialIndexes []int) (bool, int) {
	s.Lock() // Khóa GHI toàn bộ sheet này lại (Chỉ 1 người được check lúc này)
	defer s.Unlock()

	// 1. Ưu tiên tìm nick CŨ của thiết bị này trước
	for _, idx := range potentialIndexes {
		if idx >= len(s.RawValues) { continue }
		row := s.RawValues[idx]
		
		if row.DeviceId == reqDevice {
			// Tìm thấy nick cũ -> Lấy luôn!
			return true, idx
		}
	}

	// 2. Nếu không có nick cũ, tìm nick TRỐNG (Mới)
	for _, idx := range potentialIndexes {
		if idx >= len(s.RawValues) { continue }
		row := s.RawValues[idx]

		if row.DeviceId == "" {
			// Nick trống -> Ghi tên mình vào (Chiếm chỗ)
			row.DeviceId = reqDevice 
			// Vì ta đang giữ khóa s.Lock(), không ai khác có thể ghi đè lúc này.
			// Đây chính là sự "Atomic" (Nguyên tử) của Go.
			return true, idx
		}
	}

	return false, -1
}
