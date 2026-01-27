package cache

import (
	"strings"
	"sync"
	"time"

	"tiktok-server/internal/models"
)

// SheetCacheItem: Lưu cache của 1 Sheet
type SheetCacheItem struct {
	sync.RWMutex
	SpreadsheetID string
	SheetName     string
	Timestamp     time.Time
	TTL           time.Duration
	LastAccessed  time.Time

	RawValues []*models.TikTokAccount

	// --- CÁC INDEX MỚI (Sửa theo yêu cầu của bạn) ---
	IndexUserID map[string]int   // Tìm theo UserID (Cột 3)
	IndexEmail  map[string]int   // Tìm theo Email (Cột 6)
	IndexStatus map[string][]int // Tìm theo Status (Cột 0)
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
		// Khởi tạo map
		IndexUserID: make(map[string]int),
		IndexEmail:  make(map[string]int),
		IndexStatus: make(map[string][]int),
	}
}

// IsValid: Kiểm tra cache còn hạn không
func (s *SheetCacheItem) IsValid() bool {
	if len(s.RawValues) == 0 {
		return false
	}
	return time.Since(s.LastAccessed) < s.TTL
}

// BuildIndex: Xây dựng chỉ mục (Quan trọng)
func (s *SheetCacheItem) BuildIndex() {
	s.Lock()
	defer s.Unlock()

	s.IndexUserID = make(map[string]int)
	s.IndexEmail = make(map[string]int)
	s.IndexStatus = make(map[string][]int)

	for idx, acc := range s.RawValues {
		// 1. Index UserID
		if acc.UserId != "" {
			s.IndexUserID[acc.UserId] = idx
		}
		// 2. Index Email
		if acc.Email != "" {
			s.IndexEmail[strings.ToLower(acc.Email)] = idx
		}
		// 3. Index Status
		st := acc.Status
		s.IndexStatus[st] = append(s.IndexStatus[st], idx)
	}
}

// GetAccountByIndex: Lấy account an toàn
func (s *SheetCacheItem) GetAccountByIndex(idx int) *models.TikTokAccount {
	if idx >= 0 && idx < len(s.RawValues) {
		return s.RawValues[idx]
	}
	return nil
}

// UpdateAccount: Cập nhật Account
func (s *SheetCacheItem) UpdateAccount(idx int, newAcc *models.TikTokAccount) {
	s.Lock()
	defer s.Unlock()
	if idx >= 0 && idx < len(s.RawValues) {
		s.RawValues[idx] = newAcc
		s.LastAccessed = time.Now()
	}
}

// OptimisticLockingCheck: Check khóa thiết bị (Giữ nguyên logic của bạn)
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
