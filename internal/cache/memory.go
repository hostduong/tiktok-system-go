package cache

import (
	"sync"
	"time"

	"tiktok-server/internal/models" // ✅ Import theo tên module ngắn gọn
)

// SheetCacheItem: Lưu toàn bộ 1 file Excel trong RAM
type SheetCacheItem struct {
	sync.RWMutex // Khóa đa luồng (Cho phép nhiều người đọc, chỉ 1 người ghi)

	SpreadsheetID string
	SheetName     string
	Timestamp     time.Time
	TTL           time.Duration
	LastAccessed  time.Time

	// Dữ liệu chính: Mảng các con trỏ (Pointer) trỏ tới Account
	RawValues []*models.TikTokAccount

	// Các bộ chỉ mục (Index) giúp tìm kiếm siêu tốc (O(1)) thay vì duyệt mảng (O(n))
	IndexUserID   map[string]int
	IndexEmail    map[string]int
	IndexStatus   map[string][]int
}

// GlobalSheets: Kho chứa toàn bộ các Sheet đang load (Thay thế STATE.SHEET_CACHE)
var (
	GlobalSheets = sync.Map{} 
)

// NewSheetCache khởi tạo bộ nhớ cho 1 sheet mới
func NewSheetCache(sid, name string) *SheetCacheItem {
	return &SheetCacheItem{
		SpreadsheetID: sid,
		SheetName:     name,
		Timestamp:     time.Now(),
		TTL:           5 * time.Minute, // Cache sống 5 phút giống Node.js
		RawValues:     make([]*models.TikTokAccount, 0),
		IndexUserID:   make(map[string]int),
		IndexEmail:    make(map[string]int),
		IndexStatus:   make(map[string][]int),
	}
}

// OptimisticLockingCheck: Trái tim của hệ thống (Giống hệt Node.js V243)
// Trả về: (Thành công?, RowIndex)
func (s *SheetCacheItem) OptimisticLockingCheck(reqDevice string, potentialIndexes []int) (bool, int) {
	s.Lock() // 🔒 KHÓA GHI: Không ai được chen ngang lúc này
	defer s.Unlock()

	// 1. Tìm Nick Cũ (Của mình)
	for _, idx := range potentialIndexes {
		if idx >= len(s.RawValues) { continue }
		row := s.RawValues[idx]
		
		if row.DeviceId == reqDevice {
			return true, idx // Nick cũ -> Lấy luôn
		}
	}

	// 2. Tìm Nick Trống (Mới)
	for _, idx := range potentialIndexes {
		if idx >= len(s.RawValues) { continue }
		row := s.RawValues[idx]

		if row.DeviceId == "" {
			// ⚡ Ghi đè ngay lập tức (Atomic trong phạm vi Lock)
			row.DeviceId = reqDevice 
			return true, idx
		}
	}

	return false, -1
}
