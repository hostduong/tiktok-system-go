package main

import (
	"sync"
)

// Global State container
var STATE = struct {
	// Token Cache (Lớp 1 Auth)
	TokenMutex sync.RWMutex
	TokenCache map[string]*CachedToken

	// Rate Limit (Lớp 2 Auth)
	RateMutex sync.Mutex
	RateLimit map[string]*RateLimitData

	// Global Counter (Lớp 0 Auth)
	GlobalCounter struct {
		Mutex     sync.Mutex
		Count     int
		LastReset int64
	}

	// Sheet Data Cache (Core Data)
	SheetMutex sync.RWMutex
	SheetCache map[string]*SheetCacheData

	// Write Queue (Hàng đợi ghi đĩa)
	QueueMutex sync.Mutex
	WriteQueue map[string]*WriteQueueData
}{
	TokenCache: make(map[string]*CachedToken),
	RateLimit:  make(map[string]*RateLimitData),
	SheetCache: make(map[string]*SheetCacheData),
	WriteQueue: make(map[string]*WriteQueueData),
}

// Cấu trúc Cache Token
type CachedToken struct {
	IsInvalid  bool
	Msg        string
	Data       TokenData
	ExpiryTime int64
}

type TokenData struct {
	Token         string                 `json:"token"`
	SpreadsheetID string                 `json:"spreadsheetId"`
	Data          map[string]interface{} `json:"data"`
	Expired       string                 `json:"expired"`
}

type RateLimitData struct {
	Count     int
	LastReset int64
}

// 🔥 CẤU TRÚC CACHE PHÂN VÙNG (Partitioned Cache)
type SheetCacheData struct {
	RawValues   [][]interface{} // Dữ liệu gốc (Source of Truth)
	CleanValues [][]string      // Dữ liệu đã chuẩn hóa

	// 1. Map truy cập nhanh theo DeviceID (O(1))
	// Key: DeviceID -> Value: RowIndex
	// Giúp tìm nick cũ ngay lập tức mà không cần loop.
	AssignedMap map[string]int

	// 2. Danh sách Nick trống (Chưa có chủ)
	// Chỉ chứa RowIndex của các dòng có DeviceId == ""
	UnassignedList []int

	// 3. Map phân loại theo Status (để lọc nhanh nhóm "đang chờ", "đăng ký"...)
	// Key: Status -> Value: Danh sách RowIndex
	StatusMap map[string][]int

	LastAccessed int64
	Timestamp    int64
	TTL          int64
}

// Cấu trúc hàng đợi ghi
type WriteQueueData struct {
	Timer      bool // Đánh dấu đang có timer chạy flush hay không (giả lập)
	IsFlushing bool
	Updates    map[string]map[int][]interface{} // SheetName -> RowIndex -> Data
	Appends    map[string][][]interface{}       // SheetName -> List Rows
}
