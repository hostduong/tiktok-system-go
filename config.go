package main

import "regexp"

// =================================================================================================
// 🟢 CẤU HÌNH GOOGLE SHEETS & HỆ THỐNG
// =================================================================================================

const (
	SPREADSHEET_ID_MASTER = "1r71kCCd9plRqXIWKQ2-GMUp-UXH21ISmBOObbQxMZVs"
	KEY_SEPARATOR         = "__"
)

var SHEET_NAMES = struct {
	USER_NAME    string
	DATA_TIKTOK  string
	EMAIL_LOGGER string
	POST_LOGGER  string
	ERROR_LOGGER string
}{
	USER_NAME:    "UserName",
	DATA_TIKTOK:  "DataTiktok",
	EMAIL_LOGGER: "EmailLogger",
	POST_LOGGER:  "PostLogger",
	ERROR_LOGGER: "ErrorLogger",
}

var TEMPLATE_SHEETS = map[string]string{
	"DataTiktok":  "Mẫu DataTiktok",
	"EmailLogger": "Mẫu EmailLogger",
	"PostLogger":  "Mẫu PostLogger",
}

// =================================================================================================
// 🟢 CẤU HÌNH PHẠM VI DỮ LIỆU (RANGES)
// =================================================================================================

var RANGES = struct {
	DATA_START_ROW       int
	DATA_MAX_ROW         int
	EMAIL_START_ROW      int
	EMAIL_LIMIT_ROWS     int
	EMAIL_WINDOW_MINUTES int
	MAX_ROW_CLEAN        int
	DELETE_COUNT         int
	LIMIT_COL_FULL       string
}{
	DATA_START_ROW:       11,
	DATA_MAX_ROW:         10000,
	EMAIL_START_ROW:      112,
	EMAIL_LIMIT_ROWS:     500,
	EMAIL_WINDOW_MINUTES: 60,
	MAX_ROW_CLEAN:        1112,
	DELETE_COUNT:         500,
	LIMIT_COL_FULL:       "BI",
}

var CACHE = struct {
	SHEET_VALID_MS  int64
	SHEET_ERROR_MS  int64
	SHEET_MAX_KEYS  int
	TOKEN_MAX_KEYS  int
	MAIL_CACHE_TTL  int64
	TOKEN_TTL_MS    int64
	CLEAN_COL_LIMIT int
}{
	SHEET_VALID_MS:  300000, // 5 phút
	SHEET_ERROR_MS:  60000,
	SHEET_MAX_KEYS:  50,
	TOKEN_MAX_KEYS:  5000,
	MAIL_CACHE_TTL:  10000,
	TOKEN_TTL_MS:    3600000,
	CLEAN_COL_LIMIT: 61,
}

var TOKEN_RULES = struct {
	GLOBAL_MAX_REQ int
	TOKEN_MAX_REQ  int
	WINDOW_MS      int64
	MIN_LENGTH     int
	CACHE_TTL_MS   int64
	BLOCK_TTL_MS   int64
}{
	GLOBAL_MAX_REQ: 1000,
	TOKEN_MAX_REQ:  5,
	WINDOW_MS:      1000,
	MIN_LENGTH:     10,
	CACHE_TTL_MS:   3600000,
	BLOCK_TTL_MS:   60000,
}

var QUEUE = struct {
	FLUSH_INTERVAL_MS int64
	BATCH_LIMIT_BASE  int
}{
	FLUSH_INTERVAL_MS: 1000,
	BATCH_LIMIT_BASE:  500,
}

// =================================================================================================
// 🟢 BẢN ĐỒ CHỈ MỤC CỘT (INDEX MAPPING)
// =================================================================================================

var INDEX_DATA_TIKTOK = struct {
	STATUS int; NOTE int; DEVICE_ID int; USER_ID int; USER_SEC int; USER_NAME int; EMAIL int;
	NICK_NAME int; PASSWORD int; PASSWORD_EMAIL int; RECOVERY_EMAIL int; TWO_FA int;
	
	PHONE int; BIRTHDAY int; CLIENT_ID int; REFRESH_TOKEN int; ACCESS_TOKEN int;
	COOKIE int; USER_AGENT int; PROXY int; PROXY_EXPIRED int; CREATE_COUNTRY int; CREATE_TIME int;
	
	STATUS_POST int; DAILY_POST_LIMIT int; TODAY_POST_COUNT int; DAILY_FOLLOW_LIMIT int; TODAY_FOLLOW_COUNT int; LAST_ACTIVE_DATE int;
	FOLLOWER_COUNT int; FOLLOWING_COUNT int; LIKES_COUNT int; VIDEO_COUNT int; STATUS_LIVE int;
	
	LIVE_PHONE_ACCESS int; LIVE_STUDIO_ACCESS int; LIVE_KEY int; LAST_LIVE_DURATION int;
	SHOP_ROLE int; SHOP_ID int; PRODUCT_COUNT int; SHOP_HEALTH int; TOTAL_ORDERS int; TOTAL_REVENUE int; COMMISSION_RATE int;
	
	SIGNATURE int; DEFAULT_CATEGORY int; DEFAULT_PRODUCT int; PREFERRED_KEYWORDS int; PREFERRED_HASHTAGS int;
	WRITING_STYLE int; MAIN_GOAL int; DEFAULT_CTA int; CONTENT_LENGTH int; CONTENT_TYPE int;
	TARGET_AUDIENCE int; VISUAL_STYLE int; AI_PERSONA int; BANNED_KEYWORDS int; CONTENT_LANGUAGE int; COUNTRY int;
}{
	// 🔥 STATUS LÀ CỘT A -> INDEX 0 (GIỮ NGUYÊN THEO Ý BẠN)
	STATUS: 0, 
	NOTE: 1, 
	DEVICE_ID: 2, 
	USER_ID: 3, 
	USER_SEC: 4, 
	USER_NAME: 5, 
	EMAIL: 6,
	NICK_NAME: 7, PASSWORD: 8, PASSWORD_EMAIL: 9, RECOVERY_EMAIL: 10, TWO_FA: 11,
	
	PHONE: 12, BIRTHDAY: 13, CLIENT_ID: 14, REFRESH_TOKEN: 15, ACCESS_TOKEN: 16,
	COOKIE: 17, USER_AGENT: 18, PROXY: 19, PROXY_EXPIRED: 20, CREATE_COUNTRY: 21, CREATE_TIME: 22,
	
	STATUS_POST: 23, DAILY_POST_LIMIT: 24, TODAY_POST_COUNT: 25, DAILY_FOLLOW_LIMIT: 26, TODAY_FOLLOW_COUNT: 27, LAST_ACTIVE_DATE: 28,
	FOLLOWER_COUNT: 29, FOLLOWING_COUNT: 30, LIKES_COUNT: 31, VIDEO_COUNT: 32, STATUS_LIVE: 33,
	
	LIVE_PHONE_ACCESS: 34, LIVE_STUDIO_ACCESS: 35, LIVE_KEY: 36, LAST_LIVE_DURATION: 37,
	SHOP_ROLE: 38, SHOP_ID: 39, PRODUCT_COUNT: 40, SHOP_HEALTH: 41, TOTAL_ORDERS: 42, TOTAL_REVENUE: 43, COMMISSION_RATE: 44,
	
	SIGNATURE: 45, DEFAULT_CATEGORY: 46, DEFAULT_PRODUCT: 47, PREFERRED_KEYWORDS: 48, PREFERRED_HASHTAGS: 49,
	WRITING_STYLE: 50, MAIN_GOAL: 51, DEFAULT_CTA: 52, CONTENT_LENGTH: 53, CONTENT_TYPE: 54,
	TARGET_AUDIENCE: 55, VISUAL_STYLE: 56, AI_PERSONA: 57, BANNED_KEYWORDS: 58, CONTENT_LANGUAGE: 59, COUNTRY: 60,
}

// =================================================================================================
// 🟢 ĐỊNH NGHĨA TRẠNG THÁI (STATUS) - 🔥 QUAN TRỌNG: PHẢI SỬA KHÔNG DẤU
// =================================================================================================

// Trạng thái dùng để ĐỌC (SỬA THÀNH KHÔNG DẤU ĐỂ KHỚP VỚI LOG SERVER)
var STATUS_READ = struct {
	RUNNING     string
	WAITING     string
	LOGIN       string
	REGISTERING string
	WAIT_REG    string
	REGISTER    string
	COMPLETED   string
}{
	RUNNING:     "dang chay",    // Log thấy "dang chay"
	WAITING:     "dang cho",     // Log thấy "dang cho"
	LOGIN:       "dang nhap",
	REGISTERING: "dang dang ky",
	WAIT_REG:    "cho dang ky",
	REGISTER:    "dang ky",
	COMPLETED:   "hoan thanh",
}

// Trạng thái dùng để GHI (Vẫn giữ có dấu để hiển thị đẹp trên Excel)
var STATUS_WRITE = struct {
	RUNNING     string
	WAITING     string
	REGISTERING string
	WAIT_REG    string
	ATTENTION   string
}{
	RUNNING:     "Đang chạy",
	WAITING:     "Đang chờ",
	REGISTERING: "Đang đăng ký",
	WAIT_REG:    "Chờ đăng ký",
	ATTENTION:   "Chú ý",
}

var (
	REGEX_DATE = regexp.MustCompile(`(\d{1,2}\/\d{1,2}\/\d{4})`)
	REGEX_COUNT = regexp.MustCompile(`\(Lần\s*(\d+)\)`)
)
