package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tiktok-server/internal/cache"
	"tiktok-server/internal/models"
	"tiktok-server/internal/sheets"
	"tiktok-server/pkg/utils"
)

// Request Body
type LoginRequest struct {
	Type          string `json:"type"`
	Token         string `json:"token"`
	DeviceId      string `json:"deviceId"`
	Action        string `json:"action"`
	RowIndex      int    `json:"row_index"`
	SearchUserId  string `json:"search_user_id"`
	SearchUserSec string `json:"search_user_sec"`
	SearchUserName string `json:"search_user_name"`
	SearchEmail   string `json:"search_email"`
	IsReset       bool   `json:"is_reset"`
}

// Result struct for validation check
type validationResult struct {
	Valid       bool
	SystemEmail string
	Missing     string
}

func HandleLogin(w http.ResponseWriter, r *http.Request, sheetSvc *sheets.Service, spreadsheetId string) {
	// 1. Parse Body
	var body LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400)
		return
	}

	reqDevice := utils.NormalizeString(body.DeviceId)
	action := utils.NormalizeString(body.Action)

	// 2. Load Cache (Lazy Loading)
	var cacheItem *cache.SheetCacheItem
	val, ok := cache.GlobalSheets.Load(spreadsheetId + "__DataTiktok")
	if ok {
		cacheItem = val.(*cache.SheetCacheItem)
	}

	if cacheItem == nil || !cacheItem.IsValid() {
		cacheItem = cache.NewSheetCache(spreadsheetId, "DataTiktok")
		accounts, err := sheetSvc.FetchData(spreadsheetId, "DataTiktok", 11, 10000)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"status":"false","messenger":"Lỗi tải dữ liệu: %v"}`, err), 500)
			return
		}
		
		cacheItem.Lock()
		cacheItem.RawValues = accounts
		// Re-index
		for i, acc := range accounts {
			if acc.UserId != "" { cacheItem.IndexUserID[utils.NormalizeString(acc.UserId)] = i }
			if acc.UserSec != "" { cacheItem.IndexUserSec[utils.NormalizeString(acc.UserSec)] = i }
			if acc.UserName != "" { cacheItem.IndexUserName[utils.NormalizeString(acc.UserName)] = i }
			if acc.Email != "" && strings.Contains(acc.Email, "@") {
				cacheItem.IndexEmail[utils.NormalizeString(acc.Email)] = i
			}
			st := utils.NormalizeString(acc.Status)
			cacheItem.IndexStatus[st] = append(cacheItem.IndexStatus[st], i)
		}
		cacheItem.Unlock()
		cache.GlobalSheets.Store(spreadsheetId+"__DataTiktok", cacheItem)
	}

	// 3. LOGIC TÌM KIẾM
	targetIndex := -1
	responseType := "login"
	var candidates []int
	var badIndices []int // Danh sách nick lỗi để ghi log (nếu cần)

	// Search Params
	sUID := utils.NormalizeString(body.SearchUserId)
	sSec := utils.NormalizeString(body.SearchUserSec)
	sName := utils.NormalizeString(body.SearchUserName)
	sEmail := utils.NormalizeString(body.SearchEmail)
	isSearchMode := (sUID != "" || sSec != "" || sName != "" || sEmail != "")

	// 🔒 READ LOCK để tìm kiếm an toàn
	cacheItem.RLock()
	
	if isSearchMode {
		// --- SEARCH EXACT ---
		if sUID != "" {
			if idx, ok := cacheItem.IndexUserID[sUID]; ok { targetIndex = idx }
		} else if sSec != "" {
			if idx, ok := cacheItem.IndexUserSec[sSec]; ok { targetIndex = idx }
		} else if sName != "" {
			if idx, ok := cacheItem.IndexUserName[sName]; ok { targetIndex = idx }
		} else if sEmail != "" {
			if idx, ok := cacheItem.IndexEmail[sEmail]; ok { targetIndex = idx }
		}

		if targetIndex != -1 {
			acc := cacheItem.RawValues[targetIndex]
			// Check validation logic cho search mode
			st := utils.NormalizeString(acc.Status)
			currType := "login"
			if strings.Contains(st, "dang ky") || strings.Contains(st, "register") || strings.Contains(st, "wait") {
				currType = "register"
			}
			val := checkQuality(acc, currType)
			if val.Valid {
				responseType = currType
			} else {
				// Tìm thấy nhưng lỗi -> Báo lỗi (Giống Node.js logic badIndices)
				// Node.js: target.bad.push(...)
				targetIndex = -1 // Reset để báo lỗi
			}
		}
	} else {
		// --- AUTO / LOGIN / REGISTER ---
		getIdx := func(st string) []int { return cacheItem.IndexStatus[st] }
		
		type Group struct {
			Idxs []int
			Type string
			My   bool 
		}
		
		var groups []Group
		
		// Map logic Priority Groups 100% giống Node.js
		if strings.Contains(action, "login") {
			groups = []Group{
				{getIdx("dang chay"), "login", true},
				{getIdx("dang cho"), "login", true},
				{getIdx("dang nhap"), "login", true},
				{getIdx("dang nhap"), "login", false},
			}
		} else if action == "register" {
			groups = []Group{
				{getIdx("dang dang ky"), "register", true},
				{getIdx("cho dang ky"), "register", true},
				{getIdx("dang ky"), "register", true},
				{getIdx("dang ky"), "register", false},
			}
		} else if action == "auto" {
			groups = []Group{
				{getIdx("dang chay"), "login", true},
				{getIdx("dang cho"), "login", true},
				{getIdx("dang nhap"), "login", true},
				{getIdx("dang nhap"), "login", false},
				{getIdx("dang dang ky"), "register", true},
				{getIdx("cho dang ky"), "register", true},
				{getIdx("dang ky"), "register", true},
				{getIdx("dang ky"), "register", false},
			}
		}

		// Duyệt Groups
		for _, g := range groups {
			for _, idx := range g.Idxs {
				acc := cacheItem.RawValues[idx]
				devId := utils.NormalizeString(acc.DeviceId)
				isMy := (devId == reqDevice)
				isNoDev := (devId == "")

				if (g.My && isMy) || (!g.My && isNoDev) {
					// 🔥 LOGIC KIỂM TRA CHẤT LƯỢNG & SELF-HEALING 🔥
					val := checkQuality(acc, g.Type)
					
					if !val.Valid {
						// Phát hiện nick lỗi -> Đánh dấu "Chú ý" ngay lập tức (Self-healing)
						// Vì đang giữ RLock, ta cần cơ chế để update. 
						// Trong Go, acc là Pointer, nên ta có thể sửa trực tiếp field của nó.
						// Tuy nhiên, việc ghi khi đang RLock là không an toàn 100% về mặt lý thuyết Race Condition,
						// nhưng vì đây là field Status/Note ít tranh chấp nên tạm chấp nhận hoặc cần nâng cấp Lock.
						// Để an toàn tuyệt đối: Ta sẽ add vào list cần fix, và fix sau khi RUnlock.
						badIndices = append(badIndices, idx)
						continue // Bỏ qua nick lỗi, tìm nick khác
					}

					// Nick ngon -> Thêm vào danh sách ứng viên
					candidates = append(candidates, idx)
				}
			}
			if len(candidates) > 0 {
				responseType = g.Type
				break 
			}
		}
	}
	cacheItem.RUnlock()

	// 3.1 Xử lý Self-Healing (Ghi lỗi "Chú ý")
	if len(badIndices) > 0 {
		// Cần Lock ghi để update trạng thái
		// Đoạn này Node.js làm ngay trong loop, Go tách ra để tránh deadlock
		// Nhưng logic vẫn đảm bảo: Nick lỗi bị bỏ qua và bị đánh dấu.
		go func() {
			// Chạy ngầm (Goroutine) để không chặn luồng chính
			cacheItem.Lock()
			for _, idx := range badIndices {
				acc := cacheItem.RawValues[idx]
				errorMsg := "Nick thiếu thông tin" // Có thể chi tiết hơn nếu lưu msg từ checkQuality
				errorNote := fmt.Sprintf("%s\n%s", errorMsg, utils.GetVietnamTime())
				
				// Update RAM
				acc.Status = "Chú ý"
				acc.Note = errorNote
				
				// TODO: Enqueue Update to Sheet (Status + Note)
				fmt.Printf(">> [SELF-HEALING] Đánh dấu hỏng dòng %d\n", acc.RowIndex)
			}
			cacheItem.Unlock()
		}()
	}

	// 4. Optimistic Locking
	if !isSearchMode && len(candidates) > 0 {
		success, idx := cacheItem.OptimisticLockingCheck(reqDevice, candidates)
		if success {
			targetIndex = idx
		}
	}

	// 5. Kết quả
	if targetIndex == -1 {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"false","messenger":"Không còn tài khoản phù hợp"}`)
		return
	}

	// 6. Chuẩn bị dữ liệu trả về
	targetAcc := cacheItem.GetAccountByIndex(targetIndex)
	
	// System Email Logic
	systemEmail := ""
	if strings.Contains(targetAcc.Email, "@") {
		parts := strings.Split(targetAcc.Email, "@")
		if len(parts) > 1 { systemEmail = parts[1] }
	}

	// Update Status & Note
	writeStatus := "Đang chạy"
	if responseType == "register" { writeStatus = "Đang đăng ký" }
	
	newNote := utils.CreateStandardNote(targetAcc.Note, writeStatus, "normal")
	
	// Update RAM
	targetAcc.Status = writeStatus
	targetAcc.Note = newNote
	targetAcc.DeviceId = reqDevice

	// TODO: Enqueue Update to Sheet (Cột 0, 1, 2)

	// 7. Response
	authProfile, activityProfile, aiProfile := SplitProfile(targetAcc)

	resp := map[string]interface{}{
		"status":           "true",
		"type":             responseType,
		"messenger":        "Lấy nick thành công",
		"deviceId":         reqDevice,
		"row_index":        targetAcc.RowIndex,
		"system_email":     systemEmail,
		"auth_profile":     authProfile,
		"activity_profile": activityProfile,
		"ai_profile":       aiProfile,
	}
	
	if responseType == "register" {
		resp["messenger"] = "Lấy nick đăng ký thành công"
	} else {
		resp["messenger"] = "Lấy nick đăng nhập thành công"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// 🔥 Hàm checkQuality (Giống hệt Node.js kiem_tra_chat_luong_clean)
func checkQuality(acc *models.TikTokAccount, action string) validationResult {
	rawEmail := acc.Email
	systemEmail := ""
	if strings.Contains(rawEmail, "@") {
		parts := strings.Split(rawEmail, "@")
		if len(parts) > 1 { systemEmail = parts[1] }
	}

	if action == "view_only" {
		return validationResult{Valid: true, SystemEmail: systemEmail, Missing: ""}
	}

	hasEmail := (rawEmail != "")
	hasUser := (acc.UserName != "")
	hasPass := (acc.Password != "")

	if strings.Contains(action, "register") {
		if hasEmail {
			return validationResult{Valid: true, SystemEmail: systemEmail}
		}
		return validationResult{Valid: false, Missing: "email"}
	}

	if strings.Contains(action, "login") {
		if (hasEmail || hasUser) && hasPass {
			return validationResult{Valid: true, SystemEmail: systemEmail}
		}
		return validationResult{Valid: false, Missing: "user/pass"}
	}

	if action == "auto" {
		// Logic Auto: Cần Email HOẶC (User VÀ Pass)
		if hasEmail || ((hasUser || hasEmail) && hasPass) {
			return validationResult{Valid: true, SystemEmail: systemEmail}
		}
		return validationResult{Valid: false, Missing: "data"}
	}

	return validationResult{Valid: false, Missing: "unknown"}
}

func SplitProfile(acc *models.TikTokAccount) (map[string]string, map[string]string, map[string]string) {
	// ... (Giữ nguyên code SplitProfile từ bước trước)
    // Để tiết kiệm không gian chat, tôi không paste lại đoạn SplitProfile dài dòng
    // Bạn hãy dùng lại đoạn code SplitProfile ở câu trả lời trước nhé.
    // Nếu cần tôi paste lại thì báo.
    
    // Node.js: anh_xa_auth (col 0-22)
	auth := map[string]string{
		"status": acc.Status, "note": acc.Note, "device_id": acc.DeviceId,
		"user_id": acc.UserId, "user_sec": acc.UserSec, "user_name": acc.UserName,
		"email": acc.Email, "nick_name": acc.NickName, "password": acc.Password,
		"password_email": acc.PasswordEmail, "recovery_email": acc.RecoveryEmail,
		"two_fa": acc.TwoFA, "phone": acc.Phone, "birthday": acc.Birthday,
		"client_id": acc.ClientId, "refresh_token": acc.RefreshToken,
		"access_token": acc.AccessToken, "cookie": acc.Cookie,
		"user_agent": acc.UserAgent, "proxy": acc.Proxy, "proxy_expired": acc.ProxyExpired,
		"create_country": acc.CreateCountry, "create_time": acc.CreateTime,
	}

	// Node.js: anh_xa_activity (col 23-44)
	activity := map[string]string{
		"status_post": acc.StatusPost, "daily_post_limit": acc.DailyPostLimit,
		"today_post_count": acc.TodayPostCount, "daily_follow_limit": acc.DailyFollowLimit,
		"today_follow_count": acc.TodayFollowCount, "last_active_date": acc.LastActiveDate,
		"follower_count": acc.FollowerCount, "following_count": acc.FollowingCount,
		"likes_count": acc.LikesCount, "video_count": acc.VideoCount,
		"status_live": acc.StatusLive, "live_phone_access": acc.LivePhoneAccess,
		"live_studio_access": acc.LiveStudioAccess, "live_key": acc.LiveKey,
		"last_live_duration": acc.LastLiveDuration, "shop_role": acc.ShopRole,
		"shop_id": acc.ShopId, "product_count": acc.ProductCount,
		"shop_health": acc.ShopHealth, "total_orders": acc.TotalOrders,
		"total_revenue": acc.TotalRevenue, "commission_rate": acc.CommissionRate,
	}

	// Node.js: anh_xa_ai (col 45-60)
	ai := map[string]string{
		"signature": acc.Signature, "default_category": acc.DefaultCategory,
		"default_product": acc.DefaultProduct, "preferred_keywords": acc.PreferredKeywords,
		"preferred_hashtags": acc.PreferredHashtags, "writing_style": acc.WritingStyle,
		"main_goal": acc.MainGoal, "default_cta": acc.DefaultCTA,
		"content_length": acc.ContentLength, "content_type": acc.ContentType,
		"target_audience": acc.TargetAudience, "visual_style": acc.VisualStyle,
		"ai_persona": acc.AIPersona, "banned_keywords": acc.BannedKeywords,
		"content_language": acc.ContentLanguage, "country": acc.Country,
	}

	return auth, activity, ai
}
