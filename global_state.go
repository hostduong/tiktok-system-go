package main

import (
	"sync"
	"time"
)

// Cấu trúc lưu Token Cache
type TokenData struct {
	SpreadsheetID string
	Role          string
	Expired       string
	// Các trường khác từ Firebase nếu cần
}

type CachedToken struct {
	Data       TokenData
	ExpiryTime int64
	IsInvalid  bool
	Msg        string
}

// Cấu trúc lưu Sheet Cache (Dữ liệu Excel trên RAM)
type SheetCacheData struct {
	RawValues    [][]interface{}    // Dữ liệu gốc (Full cột)
	CleanValues  [][]string         // Dữ liệu sạch (7 cột đầu)
	Indices      map[string]map[string]int // Index Map (UserId -> Row, Email -> Row...)
	StatusIndices map[string][]int  // Index Status (Running -> [1, 5, 9])
	Timestamp    int64
	TTL          int64
	LastAccessed int64
	Source       string // "ram" hoặc "sheet"
    
    // 🔥 QUAN TRỌNG: Mutex riêng cho từng Sheet để Optimistic Locking
	Mutex        sync.RWMutex 
}

// Cấu trúc Hàng đợi Ghi (Write Queue)
type WriteQueueData struct {
	Timer        *time.Timer
	Updates      map[string]map[int][]interface{} // SheetName -> RowIndex -> Data
	Appends      map[string][][]interface{}       // SheetName -> List Rows
	SheetRetries map[string]int
	IsFlushing   bool
	
	Mutex        sync.Mutex // Bảo vệ Queue
}

// Cấu trúc Hàng đợi Mail (Mail Queue)
type MailQueueData struct {
	Timer      *time.Timer
	Rows       map[int]bool // Set các dòng cần update TRUE
	IsFlushing bool
	
	Mutex      sync.Mutex // Bảo vệ Mail Queue
}

// Cấu trúc Rate Limit
type RateLimitData struct {
	Count      int
	ErrorCount int
	LastReset  int64
	LastSeen   int64
	BanUntil   int64
}

// 🔥 GLOBAL STATE CONTAINER
var STATE = struct {
	TokenCache    map[string]*CachedToken
	TokenMutex    sync.RWMutex // Bảo vệ TokenCache

	SheetCache    map[string]*SheetCacheData
	SheetMutex    sync.RWMutex // Bảo vệ Map SheetCache (Thêm/Xóa file khỏi cache)

	WriteQueue    map[string]*WriteQueueData
	QueueMutex    sync.RWMutex // Bảo vệ Map WriteQueue

	MailQueue     map[string]*MailQueueData
	MailMutex     sync.RWMutex // Bảo vệ Map MailQueue

	RateLimit     map[string]*RateLimitData
	RateMutex     sync.Mutex

	GlobalCounter struct {
		LastReset int64
		Count     int
		Mutex     sync.Mutex
	}
    
    CreationLocks map[string]int64
    CreationMutex sync.Mutex
}{
	TokenCache:    make(map[string]*CachedToken),
	SheetCache:    make(map[string]*SheetCacheData),
	WriteQueue:    make(map[string]*WriteQueueData),
	MailQueue:     make(map[string]*MailQueueData),
	RateLimit:     make(map[string]*RateLimitData),
    CreationLocks: make(map[string]int64),
}
