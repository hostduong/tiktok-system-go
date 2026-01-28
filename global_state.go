package main

import (
	"sync"
	"time"
)

// 🔥 CẬP NHẬT: Struct TokenData chuẩn (Hợp nhất nhu cầu của main.go và handlers)
type TokenData struct {
	Token         string                 // Token chuỗi
	SpreadsheetID string                 // ID Google Sheet
	Data          map[string]interface{} // Dữ liệu thô từ Firebase
	Role          string                 // Vai trò (nếu có)
	Expired       string                 // Ngày hết hạn
}

// Struct kết quả Auth (Dùng chung cho service_auth và handlers)
type AuthResult struct {
	IsValid       bool
	Messenger     string
	SpreadsheetID string
	Data          map[string]interface{}
}

type CachedToken struct {
	Data       TokenData
	ExpiryTime int64
	IsInvalid  bool
	Msg        string
}

// Cấu trúc lưu Sheet Cache (Dữ liệu Excel trên RAM)
type SheetCacheData struct {
	RawValues     [][]interface{}
	CleanValues   [][]string
	Indices       map[string]map[string]int
	StatusIndices map[string][]int
	Timestamp     int64
	TTL           int64
	LastAccessed  int64
	Source        string
	Mutex         sync.RWMutex
}

// Cấu trúc Hàng đợi Ghi (Write Queue)
type WriteQueueData struct {
	Timer        *time.Timer
	Updates      map[string]map[int][]interface{}
	Appends      map[string][][]interface{}
	SheetRetries map[string]int
	IsFlushing   bool
	Mutex        sync.Mutex
}

// Cấu trúc Hàng đợi Mail (Mail Queue)
type MailQueueData struct {
	Timer      *time.Timer
	Rows       map[int]bool
	IsFlushing bool
	Mutex      sync.Mutex
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
	TokenMutex    sync.RWMutex

	SheetCache    map[string]*SheetCacheData
	SheetMutex    sync.RWMutex

	WriteQueue    map[string]*WriteQueueData
	QueueMutex    sync.RWMutex

	MailQueue     map[string]*MailQueueData
	MailMutex     sync.RWMutex

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
