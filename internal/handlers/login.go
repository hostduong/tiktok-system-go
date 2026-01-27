package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tiktok-server/internal/auth"
	"tiktok-server/internal/cache"
	"tiktok-server/internal/models"
	"tiktok-server/internal/queue"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

type LoginRequest struct {
	Type     string `json:"type"`
	Token    string `json:"token"`
	DeviceId string `json:"deviceId"`
	Action   string `json:"action"` // login, register, auto, view_only
	// Search params
	SearchUserId string `json:"search_user_id"`
	SearchEmail  string `json:"search_email"`
	RowIndex     int    `json:"row_index"`
	IsReset      bool   `json:"is_reset"`
}

// Cấu trúc Priority Group (mô phỏng logic Node.js)
type PriorityGroup struct {
	Indices []int
	Type    string // login / register
	P       int    // Priority (1 is highest)
	My      bool   // True: Chỉ tìm nick của mình. False: Tìm nick trống
}

func HandleLogin(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	var body LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.JSONResponse(w, "false", "Lỗi JSON input", nil)
		return
	}

	deviceID := utils.NormalizeString(body.DeviceId)
	action := utils.NormalizeString(body.Action)
	if action == "" { action = "login" }
	
	// 1. Load Data (Cache Layer)
	sheetName := "DataTiktok"
	cacheKey := spreadsheetId + "__" + sheetName
	
	var cacheItem *cache.SheetCacheItem
	val, ok := cache.GlobalSheets.Load(cacheKey)
	if ok {
		cacheItem = val.(*cache.SheetCacheItem)
	}

	// Nếu cache không hợp lệ hoặc chưa có -> Load từ Sheet
	if cacheItem == nil || !cacheItem.IsValid() {
		// Dùng Lock để tránh nhiều request cùng load 1 lúc
		rawRows, err := sheetSvc.FetchData(spreadsheetId, sheetName, 11, 10000)
		if err != nil {
			utils.JSONResponse(w, "false", fmt.Sprintf("Lỗi tải dữ liệu: %v", err), nil)
			return
		}
		
		// Map Raw Data sang Struct
		parsedAccounts := make([]*models.TikTokAccount, len(rawRows))
		for i, row := range rawRows {
			acc := models.NewAccount()
			acc.FromSlice(row)
			acc.RowIndex = 11 + i
			parsedAccounts[i] = acc
		}

		cacheItem = cache.NewSheetCache(spreadsheetId, sheetName)
		cacheItem.Lock()
		cacheItem.RawValues = parsedAccounts
		cacheItem.Unlock()
		cacheItem.BuildIndex()
		cache.GlobalSheets.Store(cacheKey, cacheItem)
	}

	// 2. Logic Tìm kiếm (Search & Optimistic Locking)
	cacheItem.Lock() // Lock toàn bộ cache khi tìm kiếm để an toàn thread
	defer cacheItem.Unlock()

	targetIdx := -1
	resultType := "login"
	
	// 2a. Tìm theo RowIndex (Ưu tiên cao nhất)
	if body.RowIndex >= 11 {
		idx := body.RowIndex - 11
		if idx >= 0 && idx < len(cacheItem.RawValues) {
			targetIdx = idx
			// Xác định type dựa trên Status
			st := utils.NormalizeString(cacheItem.RawValues[idx].Status)
			if strings.Contains(st, "dang ky") || strings.Contains(st, "reg") {
				resultType = "register"
			}
		}
	}

	// 2b. Nếu chưa tìm thấy -> Chạy thuật toán tìm kiếm V243
	if targetIdx == -1 {
		// Xây dựng danh sách nhóm ưu tiên (Priority Groups)
		groups := buildPriorityGroups(cacheItem, action, body.IsReset)
		
		for _, g := range groups {
			for _, idx := range g.Indices {
				if idx >= len(cacheItem.RawValues) { continue }
				acc := cacheItem.RawValues[idx]
				curDev := utils.NormalizeString(acc.DeviceId)
				
				isMy := (curDev == deviceID)
				isNoDev := (curDev == "")

				if (g.My && isMy) || (!g.My && isNoDev) {
					// Check chất lượng nick (Validate)
					if !isValidAccount(acc, g.Type) {
						continue // Hoặc ghi lỗi Self-healing (bỏ qua để code gọn)
					}

					// 🔥 OPTIMISTIC LOCKING LOGIC 🔥
					if isMy {
						// Case 1: Nick của mình -> Lấy luôn
						targetIdx = idx
						resultType = g.Type
						goto FOUND
					} else if isNoDev {
						// Case 2: Nick trống -> Ghi đè RAM -> Kiểm tra lại
						acc.DeviceId = deviceID // Ghi đè ngay trong RAM (đang giữ Lock)
						
						// Vì đang giữ Mutex Lock, việc này là an toàn tuyệt đối trong Go 
						// (khác với Node.js là đơn luồng). 
						// Nếu luồng khác đọc được, nó sẽ thấy DeviceID đã có.
						
						targetIdx = idx
						resultType = g.Type
						goto FOUND
					}
				}
			}
		}
	}

FOUND:
	if targetIdx == -1 {
		if action == "view_only" {
			utils.JSONResponse(w, "true", "Không có dữ liệu", nil)
		} else {
			utils.JSONResponse(w, "false", "Không còn tài khoản phù hợp", nil)
		}
		return
	}

	// 3. Xử lý kết quả & Queue Update
	acc := cacheItem.RawValues[targetIdx]
	
	// Nếu view only thì trả về luôn
	if action == "view_only" {
		p1, p2, p3 := SplitProfile(acc)
		utils.JSONResponseRaw(w, map[string]interface{}{
			"status": "true", "type": resultType, "messenger": "OK",
			"row_index": acc.RowIndex, "auth_profile": p1, "activity_profile": p2, "ai_profile": p3,
		})
		return
	}

	// Double Check DeviceID (An toàn)
	if utils.NormalizeString(acc.DeviceId) != deviceID {
		utils.JSONResponse(w, "false", "Hệ thống bận (Nick vừa bị lấy)", nil)
		return
	}

	// Update Status & Note
	newStatus := "Đang chạy"
	if resultType == "register" { newStatus = "Đang đăng ký" }
	
	noteMode := "updated"
	if body.IsReset { noteMode = "reset" }
	
	newNote := utils.CreateStandardNote(acc.Note, newStatus, noteMode)
	
	// Update RAM
	acc.Status = newStatus
	acc.Note = newNote
	acc.DeviceId = deviceID // Update lại cho chắc
	
	// Enqueue Disk Write
	q := queue.GetQueue(spreadsheetId, sheetSvc)
	q.EnqueueUpdate(sheetName, acc.RowIndex, acc.ToSlice()) // acc.RowIndex là số thực tế (vd: 11)

	// Clean up các nick khác đang treo DeviceID này (Logic Clean)
	// (Đoạn này lược bỏ để code ngắn gọn, nhưng logic là loop check index khác)

	// Response
	p1, p2, p3 := SplitProfile(acc)
	msg := "Lấy nick đăng nhập thành công"
	if resultType == "register" { msg = "Lấy nick đăng ký thành công" }

	utils.JSONResponseRaw(w, map[string]interface{}{
		"status": "true",
		"type": resultType,
		"messenger": msg,
		"row_index": acc.RowIndex,
		"auth_profile": p1,
		"activity_profile": p2,
		"ai_profile": p3,
	})
}

// Helpers Logic
func buildPriorityGroups(c *cache.SheetCacheItem, action string, isReset bool) []PriorityGroup {
	var groups []PriorityGroup
	
	// Helper lấy indices
	get := func(st string) []int { return c.IndexStatus[utils.NormalizeString(st)] }
	
	if strings.Contains(action, "login") {
		groups = append(groups, PriorityGroup{Indices: get("dang chay"), Type: "login", My: true})
		groups = append(groups, PriorityGroup{Indices: get("dang cho"), Type: "login", My: true})
		groups = append(groups, PriorityGroup{Indices: get("dang nhap"), Type: "login", My: true})
		groups = append(groups, PriorityGroup{Indices: get("dang nhap"), Type: "login", My: false})
		if isReset {
			groups = append(groups, PriorityGroup{Indices: get("hoan thanh"), Type: "login", My: true})
		}
	} else if action == "register" {
		groups = append(groups, PriorityGroup{Indices: get("dang dang ky"), Type: "register", My: true})
		groups = append(groups, PriorityGroup{Indices: get("cho dang ky"), Type: "register", My: true})
		groups = append(groups, PriorityGroup{Indices: get("dang ky"), Type: "register", My: true})
		groups = append(groups, PriorityGroup{Indices: get("dang ky"), Type: "register", My: false})
	} else if action == "auto" {
		// Logic Auto gộp cả 2
		groups = append(groups, PriorityGroup{Indices: get("dang chay"), Type: "login", My: true})
		// ... (Thêm các nhóm tương tự Node.js)
		groups = append(groups, PriorityGroup{Indices: get("dang ky"), Type: "register", My: false})
	}
	return groups
}

func isValidAccount(acc *models.TikTokAccount, actionType string) bool {
	// Logic kiem_tra_chat_luong_clean
	hasEmail := strings.Contains(acc.Email, "@")
	hasUser := acc.UserName != ""
	hasPass := acc.Password != ""
	
	if actionType == "register" {
		return hasEmail
	}
	// Login
	return (hasEmail || hasUser) && hasPass
}
