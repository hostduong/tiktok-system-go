package main

import "sync"

var STATE = struct {
	TokenMutex sync.RWMutex
	TokenCache map[string]*CachedToken

	RateMutex sync.Mutex
	RateLimit map[string]*RateLimitData

	GlobalCounter struct {
		Mutex     sync.Mutex
		Count     int
		LastReset int64
	}

	SheetMutex sync.RWMutex
	SheetCache map[string]*SheetCacheData

	QueueMutex sync.Mutex
	WriteQueue map[string]*WriteQueueData
}{
	TokenCache: make(map[string]*CachedToken),
	RateLimit:  make(map[string]*RateLimitData),
	SheetCache: make(map[string]*SheetCacheData),
	WriteQueue: make(map[string]*WriteQueueData),
}

// Struct kết quả Auth (Fix lỗi undefined)
type AuthResult struct {
	IsValid       bool
	Messenger     string
	SpreadsheetID string
	Data          map[string]interface{}
}

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

// 🔥 CẤU TRÚC CACHE PHÂN VÙNG (PARTITIONED)
type SheetCacheData struct {
	RawValues      [][]interface{}
	CleanValues    [][]string
	AssignedMap    map[string]int   // Key: DeviceID -> Value: RowIndex
	UnassignedList []int            // List RowIndex chưa có chủ
	StatusMap      map[string][]int // Key: Status -> List RowIndex
	LastAccessed   int64
	Timestamp      int64
	TTL            int64
}

type WriteQueueData struct {
	Timer      bool
	IsFlushing bool
	Updates    map[string]map[int][]interface{}
	Appends    map[string][][]interface{}
}
