package main

import "regexp"

// ============================================================
// 🟢 CẤU HÌNH GOOGLE SHEETS & HỆ THỐNG
// ============================================================

const (
	// ID của file Google Sheet Master (File mẫu hoặc file quản lý chính)
	// Đây là chuỗi ký tự nằm trên URL của Google Sheet
	SPREADSHEET_ID_MASTER = "1r71kCCd9plRqXIWKQ2-GMUp-UXH21ISmBOObbQxMZVs"
	
	// Ký tự phân cách khi tạo Key cho Cache (Ví dụ: SheetID__SheetName)
	KEY_SEPARATOR = "__"
)

// Danh sách tên các Sheet (Tab) trong file Excel
var SHEET_NAMES = struct {
	USER_NAME    string // Sheet chứa danh sách User sử dụng tool
	DATA_TIKTOK  string // Sheet chứa dữ liệu nick TikTok (quan trọng nhất)
	EMAIL_LOGGER string // Sheet ghi log OTP/Email gửi về
	POST_LOGGER  string // Sheet ghi log lịch sử đăng bài
	ERROR_LOGGER string // Sheet ghi log lỗi hệ thống
}{
	USER_NAME:    "UserName",
	DATA_TIKTOK:  "DataTiktok",
	EMAIL_LOGGER: "EmailLogger",
	POST_LOGGER:  "PostLogger",
	ERROR_LOGGER: "ErrorLogger",
}

// Map dùng để copy sheet mẫu khi tạo mới cho user
// Key: Tên sheet hệ thống - Value: Tên sheet mẫu trong file Master
var TEMPLATE_SHEETS = map[string]string{
	"DataTiktok":  "Mẫu DataTiktok",
	"EmailLogger": "Mẫu EmailLogger",
	"PostLogger":  "Mẫu PostLogger",
}

// ============================================================
// 🟢 CẤU HÌNH PHẠM VI DỮ LIỆU (RANGES)
// ============================================================

var RANGES = struct {
	DATA_START_ROW       int    // Dòng bắt đầu chứa dữ liệu nick (thường bỏ qua Header)
	DATA_MAX_ROW         int    // Giới hạn số dòng tối đa đọc để tránh quá tải RAM
	EMAIL_START_ROW      int    // Dòng bắt đầu ghi log Email
	EMAIL_LIMIT_ROWS     int    // Số lượng mail tối đa xử lý 1 lần
	EMAIL_WINDOW_MINUTES int    // Chỉ quét mail trong khoảng thời gian này (phút) đổ lại
	MAX_ROW_CLEAN        int    // Ngưỡng số dòng để kích hoạt dọn dẹp file Log
	DELETE_COUNT         int    // Số dòng sẽ xóa mỗi khi dọn dẹp
	LIMIT_COL_FULL       string // Tên cột cuối cùng của bảng dữ liệu (Ví dụ: BI)
}{
	DATA_START_ROW:       11,    // Dữ liệu bắt đầu từ dòng 11
	DATA_MAX_ROW:         10000, // Đọc tối đa 10.000 nick
	EMAIL_START_ROW:      112,   // Log mail bắt đầu từ dòng 112
	EMAIL_LIMIT_ROWS:     500,   // Đọc 500 mail gần nhất
	EMAIL_WINDOW_MINUTES: 60,    // Chỉ lấy mail trong 60 phút gần nhất
	MAX_ROW_CLEAN:        1112,  // Nếu log vượt quá 1112 dòng thì dọn dẹp
	DELETE_COUNT:         500,   // Xóa bớt 500 dòng cũ
	LIMIT_COL_FULL:       "BI",  // Cột BI tương ứng với index 60 (tổng 61 cột)
}

// ============================================================
// 🟢 CẤU HÌNH CACHE & PERFORMANCE
// ============================================================

var CACHE = struct {
	SHEET_VALID_MS  int64 // Thời gian Cache dữ liệu Sheet (ms) - 5 phút
	SHEET_ERROR_MS  int64 // Thời gian Cache lỗi (tránh retry liên tục) - 1 phút
	SHEET_MAX_KEYS  int   // Số lượng Sheet tối đa lưu trong RAM
	TOKEN_MAX_KEYS  int   // Số lượng Token User tối đa lưu trong RAM
	MAIL_CACHE_TTL  int64 // Thời gian Cache kết quả đọc Mail - 10 giây
	TOKEN_TTL_MS    int64 // Thời gian sống của Token - 1 giờ
	CLEAN_COL_LIMIT int   // Số cột tối đa cần "làm sạch" (Trim/Lowercase) để search nhanh
}{
	SHEET_VALID_MS:  300000,  // 300,000ms = 5 phút
	SHEET_ERROR_MS:  60000,   // 60,000ms = 1 phút
	SHEET_MAX_KEYS:  50,      // Cache 50 file Excel
	TOKEN_MAX_KEYS:  5000,    // Cache 5000 user
	MAIL_CACHE_TTL:  10000,   // 10s
	TOKEN_TTL_MS:    3600000, // 1 giờ
	CLEAN_COL_LIMIT: 61,      // Cache sạch 61 cột
}

// Cấu hình hàng đợi ghi dữ liệu (Write Queue) để tránh lỗi "Too Many Requests" từ Google
var QUEUE = struct {
	FLUSH_INTERVAL_MS int64 // Thời gian xả hàng đợi ghi xuống đĩa (3 giây/lần)
	BATCH_LIMIT_BASE  int   // Số lượng dòng tối đa ghi 1 lần
}{
	FLUSH_INTERVAL_MS: 1000, // 3 giây
	BATCH_LIMIT_BASE:  500,  // 500 dòng
}

// ============================================================
// 🟢 MAPPING CHỈ SỐ CỘT (INDEX) - QUAN TRỌNG NHẤT
// ============================================================
// Định nghĩa vị trí các cột trong file Excel (Bắt đầu từ 0)

var INDEX_DATA_TIKTOK = struct {
	// --- Nhóm 1: Thông tin cơ bản & Login ---
	STATUS int; NOTE int; DEVICE_ID int; USER_ID int; USER_SEC int; USER_NAME int; EMAIL int;
	NICK_NAME int; PASSWORD int; PASSWORD_EMAIL int; RECOVERY_EMAIL int; TWO_FA int;
	
	// --- Nhóm 2: Thông tin thiết bị & Cookies ---
	PHONE int; BIRTHDAY int; CLIENT_ID int; REFRESH_TOKEN int; ACCESS_TOKEN int;
	COOKIE int; USER_AGENT int; PROXY int; PROXY_EXPIRED int; CREATE_COUNTRY int; CREATE_TIME int;
	
	// --- Nhóm 3: Chỉ số hoạt động (KPIs) ---
	STATUS_POST int; DAILY_POST_LIMIT int; TODAY_POST_COUNT int; DAILY_FOLLOW_LIMIT int; TODAY_FOLLOW_COUNT int; LAST_ACTIVE_DATE int;
	FOLLOWER_COUNT int; FOLLOWING_COUNT int; LIKES_COUNT int; VIDEO_COUNT int; STATUS_LIVE int;
	
	// --- Nhóm 4: Livestream ---
	LIVE_PHONE_ACCESS int; LIVE_STUDIO_ACCESS int; LIVE_KEY int; LAST_LIVE_DURATION int;
	
	// --- Nhóm 5: TikTok Shop & Affiliate ---
	SHOP_ROLE int; SHOP_ID int; PRODUCT_COUNT int; SHOP_HEALTH int; TOTAL_ORDERS int; TOTAL_REVENUE int; COMMISSION_RATE int;
	
	// --- Nhóm 6: Cấu hình Nội dung & AI ---
	SIGNATURE int; DEFAULT_CATEGORY int; DEFAULT_PRODUCT int; PREFERRED_KEYWORDS int; PREFERRED_HASHTAGS int;
	WRITING_STYLE int; MAIN_GOAL int; DEFAULT_CTA int; CONTENT_LENGTH int; CONTENT_TYPE int;
	TARGET_AUDIENCE int; VISUAL_STYLE int; AI_PERSONA int; BANNED_KEYWORDS int; CONTENT_LANGUAGE int; COUNTRY int;
}{
	// Khởi tạo giá trị Index (Cột A = 0, B = 1, ...)
	STATUS: 0, NOTE: 1, DEVICE_ID: 2, USER_ID: 3, USER_SEC: 4, USER_NAME: 5, EMAIL: 6,
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

// ============================================================
// 🟢 ĐỊNH NGHĨA TRẠNG THÁI (STATUS)
// ============================================================

// Các trạng thái hệ thống dùng để ĐỌC và so sánh logic (viết thường)
var STATUS_READ = struct {
	RUNNING     string
	WAITING     string
	LOGIN       string
	REGISTERING string
	WAIT_REG    string
	REGISTER    string
	COMPLETED   string
}{
	RUNNING:     "đang chạy",
	WAITING:     "đang chờ",
	LOGIN:       "đăng nhập",
	REGISTERING: "đang đăng ký",
	WAIT_REG:    "chờ đăng ký",
	REGISTER:    "đăng ký",
	COMPLETED:   "hoàn thành",
}

// Các trạng thái dùng để GHI vào file Excel (Viết hoa đẹp để user xem)
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
	ATTENTION:   "Chú ý", // Dùng khi nick bị lỗi hoặc cần check tay
}

// ============================================================
// 🟢 REGEX PATTERNS
// ============================================================

var (
	// Regex nhận diện ngày tháng: dd/mm/yyyy
	REGEX_DATE = regexp.MustCompile(`(\d{1,2}\/\d{1,2}\/\d{4})`)
	
	// Regex nhận diện số lần chạy trong ghi chú: (Lần 5)
	REGEX_COUNT = regexp.MustCompile(`\(Lần\s*(\d+)\)`)
)
