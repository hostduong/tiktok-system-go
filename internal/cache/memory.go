package cache

import (
	"strings"
	"sync"
	"time"

	"tiktok-server/internal/models"
)

// SheetCacheItem: Lưu toàn bộ 1 file Excel trong RAM
type SheetCacheItem struct {
	sync.RWMutex

	SpreadsheetID string
	SheetName     string
	Timestamp     time.Time
	TTL           time.Duration
	LastAccessed  time.Time

	RawValues []*models.TikTokAccount

	// Các Index để tìm kiếm nhanh
	IndexUserSec  map[string]int // Login Handler cần cái này
	IndexUserName map[string]int // Login Handler cần cái này
}

var GlobalSheets = sync.Map{}

func NewSheetCache(sid, name string) *SheetCacheItem {
	return &SheetCacheItem{
		SpreadsheetID: sid,
		SheetName:     name,
		Timestamp:     time.Now(),
		LastAccessed:  time.Now(),
		TTL:           5 * time.Minute,
		RawValues:     make([]*models.TikTokAccount, 0),
		IndexUserSec:  make(map[string]int),
		IndexUserName: make(map[string]int),
	}
}

// IsValid: Kiểm tra Cache còn hạn không
func (s *SheetCacheItem) IsValid() bool {
	if len(s.RawValues) == 0 {
		return false
	}
	return time.Since(s.LastAccessed) < s.TTL
}

// BuildIndex: Tạo chỉ mục tìm kiếm (Map)
func (s *SheetCacheItem) BuildIndex() {
	s.Lock()
	defer s.Unlock()

	s.IndexUserSec = make(map[string]int)
	s.IndexUserName = make(map[string]int)

	for idx, acc := range s.RawValues {
		// Index 1: User|Pass|Email
		key := strings.ToLower(acc.UserId + "|" + acc.Password + "|" + acc.Email)
		s.IndexUserSec[key] = idx

		// Index 2: Username
		if acc.UserName != "" {
			s.IndexUserName[strings.ToLower(acc.UserName)] = idx
		}
	}
}

// OptimisticLockingCheck: Check khóa thiết bị
func (s *SheetCacheItem) OptimisticLockingCheck(reqDevice string, potentialIndexes []int) (bool, int) {
	s.Lock()
	defer s.Unlock()

	// 1. Tìm Nick Cũ
	for _, idx := range potentialIndexes {
		if idx >= len(s.RawValues) {
			continue
		}
		row := s.RawValues[idx]
		if row.DeviceId == reqDevice {
			return true, idx
		}
	}

	// 2. Tìm Nick Trống
	for _, idx := range potentialIndexes {
		if idx >= len(s.RawValues) {
			continue
		}
		row := s.RawValues[idx]
		if row.DeviceId == "" {
			row.DeviceId = reqDevice
			return true, idx
		}
	}

	return false, -1
}

// GetAccountByIndex: Lấy account an toàn
func (s *SheetCacheItem) GetAccountByIndex(idx int) *models.TikTokAccount {
	if idx >= 0 && idx < len(s.RawValues) {
		return s.RawValues[idx]
	}
	return nil
}

// UpdateAccount: Cập nhật Account trong cache
func (s *SheetCacheItem) UpdateAccount(idx int, newAcc *models.TikTokAccount) {
	s.Lock()
	defer s.Unlock()
	if idx >= 0 && idx < len(s.RawValues) {
		s.RawValues[idx] = newAcc
		s.LastAccessed = time.Now()
	}
}
