package cache

import (
	"sync"
	"time"

	"tiktok-server/internal/models" // ✅ Đã sửa theo tên module chung
)

// SheetCacheItem lưu trữ dữ liệu của 1 file Excel trong RAM
type SheetCacheItem struct {
	sync.RWMutex // Khóa an toàn

	SpreadsheetID string
	SheetName     string
	Timestamp     time.Time
	TTL           time.Duration
	LastAccessed  time.Time

	// Dữ liệu chính
	RawValues []*models.TikTokAccount

	// Index
	IndexUserID   map[string]int
	IndexEmail    map[string]int
	IndexStatus   map[string][]int
	IndexDeviceId map[string][]int
}

// GlobalCache: Kho chứa toàn bộ các Sheet đang load
// Dùng sync.Map để an toàn luồng (Concurrent Safe) mà không cần tự lock Map cha
var (
	GlobalSheets = sync.Map{} 
)

// NewSheetCache tạo một cache mới
func NewSheetCache(sid, name string) *SheetCacheItem {
	return &SheetCacheItem{
		SpreadsheetID: sid,
		SheetName:     name,
		Timestamp:     time.Now(),
		TTL:           5 * time.Minute,
		RawValues:     make([]*models.TikTokAccount, 0),
		IndexUserID:   make(map[string]int),
		IndexEmail:    make(map[string]int),
		IndexStatus:   make(map[string][]int),
		IndexDeviceId: make(map[string][]int),
	}
}

// IsValid kiểm tra hạn dùng
func (s *SheetCacheItem) IsValid() bool {
	s.RLock()
	defer s.RUnlock()
	return time.Since(s.Timestamp) < s.TTL
}

// GetAccountByIndex lấy dữ liệu dòng cụ thể
func (s *SheetCacheItem) GetAccountByIndex(idx int) *models.TikTokAccount {
	s.RLock()
	defer s.RUnlock()
	if idx < 0 || idx >= len(s.RawValues) {
		return nil
	}
	return s.RawValues[idx]
}

// UpdateAccount cập nhật RAM (Cơ chế Merge sẽ xử lý ở tầng Handler, tầng này chỉ Ghi đè)
func (s *SheetCacheItem) UpdateAccount(idx int, newData *models.TikTokAccount) {
	s.Lock()
	defer s.Unlock()

	if idx < 0 || idx >= len(s.RawValues) {
		return
	}
	s.RawValues[idx] = newData
	s.LastAccessed = time.Now()
    
    // TODO: Update Index (Sẽ bổ sung logic cập nhật IndexMap sau)
}

// OptimisticLockingCheck: Trái tim của hệ thống
// Trả về: (Thành công?, RowIndex)
func (s *SheetCacheItem) OptimisticLockingCheck(reqDevice string, potentialIndexes []int) (bool, int) {
	s.Lock() // 🔒 KHÓA GHI TOÀN BỘ SHEET (Chỉ 1 người được chạy đoạn này)
	defer s.Unlock()

	// 1. Tìm Nick Cũ
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
