package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/sheets/v4"
)

/*
=================================================================================================
📘 TÀI LIỆU API: MAIL LOGGER & OTP (POST /tool/mail_log & /tool/read_mail)
=================================================================================================

1. API GHI LOG MAIL (POST /tool/mail_log)
   - Chức năng: Nhận log từ tool nuôi nick (Email, Pass, Subject, OTP...) và ghi vào Google Sheet.
   - Tự động: Kích hoạt chế độ dọn dẹp (Cleanup) nếu phát hiện file quá nặng.
   - Cấu trúc Body:
     {
       "token": "...",
       "data": [
         {
           "sheet": "EmailLogger", // Tên sheet (Mặc định: EmailLogger)
           "col_0": "...",         // Cột A: Thời gian (Serial Date)
           "col_1": "...",         // Cột B: Sender Name
           "col_2": "...",         // Cột C: Receiver Email (Email nhận)
           "col_3": "...",         // Cột D: Sender Email
           "col_6": "123456"       // Cột G: Mã OTP/Code
         }
       ]
     }

2. API ĐỌC MAIL/OTP (POST /tool/read_mail)
   - Chức năng: Tìm kiếm mã OTP trong RAM với tốc độ cao.
   - Nguyên tắc: Lấy mail MỚI NHẤT (Quét từ dưới lên).
   - Cấu trúc Body:
     { 
       "token": "...",             // Token xác thực
       "email": "abc@hotmail.com", // Email cần lấy mã
       "keyword": "Verify",        // (Tùy chọn) Lọc theo từ khóa (Sender hoặc Subject)
       "read": true                // true = Đánh dấu đã dùng (Để tool khác không lấy trùng lại)
     }

3. CƠ CHẾ DỌN DẸP THÔNG MINH (SMART CLEANUP):
   - Khi số dòng vượt quá ngưỡng (Ví dụ: 1112 dòng), hệ thống sẽ:
     + Bước 1: Ghi hết dữ liệu đang chờ xuống đĩa (Flush Queue).
     + Bước 2: Cắt bớt 500 dòng cũ nhất trong RAM.
     + Bước 3: Gửi lệnh xóa 500 dòng cũ nhất trên Google Sheet (Chạy ngầm).
*/

// =================================================================================================
// 🟢 API 1: GHI LOG MAIL
// =================================================================================================

func HandleMailData(w http.ResponseWriter, r *http.Request) {
	// 1. Giải mã JSON
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"Lỗi cấu trúc JSON"}`, 400); return
	}

	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	// 2. Phân tích dữ liệu đầu vào
	dataList, _ := body["data"].([]interface{})
	rowsBySheet := make(map[string][][]interface{})
	hasEmailLog := false

	for _, item := range dataList {
		obj, ok := item.(map[string]interface{})
		if !ok { continue }
		
		// Xác định tên Sheet
		sheet := SHEET_NAMES.EMAIL_LOGGER
		if s, ok := obj["sheet"].(string); ok && s != "" { sheet = s }
		
		// Đánh dấu xem request này có đụng vào EmailLogger không (để kích hoạt cleanup)
		if sheet == SHEET_NAMES.EMAIL_LOGGER { hasEmailLog = true }

		// Tìm cột lớn nhất để khởi tạo mảng (tránh index out of range)
		maxCol := 0
		for k := range obj {
			if strings.HasPrefix(k, "col_") {
				if idx, err := strconv.Atoi(k[4:]); err == nil && idx > maxCol { maxCol = idx }
			}
		}
		// Giới hạn cột an toàn (Max 20 cột cho nhẹ)
		if maxCol > 20 { maxCol = 20 } 

		// Tạo dòng dữ liệu
		row := make([]interface{}, maxCol+1)
		for i := range row { row[i] = "" }
		
		for k, v := range obj {
			if strings.HasPrefix(k, "col_") {
				if idx, err := strconv.Atoi(k[4:]); err == nil && idx <= maxCol { row[idx] = v }
			}
		}
		rowsBySheet[sheet] = append(rowsBySheet[sheet], row)
	}

	// 3. Đẩy xuống Queue (Hàng đợi ghi đĩa)
	for s, r := range rowsBySheet {
		if len(r) > 0 { QueueAppend(tokenData.SpreadsheetID, s, r) }
	}

	// 🔥 4. Kích hoạt dọn dẹp nếu cần (Logic thông minh)
	// Chỉ kiểm tra khi có log mới vào sheet EmailLogger.
	if hasEmailLog {
		// Kiểm tra nhanh độ dài RAM (Thread-safe read)
		STATE.SheetMutex.RLock()
		key := tokenData.SpreadsheetID + KEY_SEPARATOR + SHEET_NAMES.EMAIL_LOGGER
		cached, exists := STATE.SheetCache[key]
		currentLen := 0
		if exists { currentLen = len(cached.RawValues) }
		STATE.SheetMutex.RUnlock()

		// Nếu vượt ngưỡng -> Chạy dọn dẹp ngầm (Goroutine)
		// Ngưỡng = Max (1112) - Start (112) = 1000 dòng
		threshold := RANGES.MAX_ROW_CLEAN - RANGES.EMAIL_START_ROW
		if currentLen > threshold {
			go CleanupOldMails(tokenData.SpreadsheetID)
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "true", "messenger": "Đã tiếp nhận mail log"})
}

// =================================================================================================
// 🟢 API 2: ĐỌC MAIL/OTP
// =================================================================================================

func HandleReadMail(w http.ResponseWriter, r *http.Request) {
	// 1. Parse Body Request
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"status":"false","messenger":"JSON Error"}`, 400); return
	}
	tokenData, ok := r.Context().Value("tokenData").(*TokenData)
	if !ok { return }

	sid := tokenData.SpreadsheetID
	email := CleanString(body["email"])
	keyword := CleanString(body["keyword"])
	markRead := fmt.Sprintf("%v", body["read"]) == "true" // Có đánh dấu đã đọc không?

	// 2. Load Dữ liệu từ RAM (Rất nhanh)
	cacheData, err := LayDuLieu(sid, SHEET_NAMES.EMAIL_LOGGER, false)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"status": "false", "messenger": "Lỗi đọc dữ liệu"})
		return
	}

	STATE.SheetMutex.RLock() // 🔒 Khóa ĐỌC
	rows := cacheData.RawValues
	
	var resultData map[string]interface{}
	found := false
	targetIdx := -1
	foundCode := "" // Lưu lại Code để double check sau này
	
	// Giới hạn thời gian (Chỉ lấy mail trong X phút gần nhất)
	limitTime := time.Now().Add(time.Duration(-RANGES.EMAIL_WINDOW_MINUTES) * time.Minute).UnixMilli()
	processCount := 0
	
	// 3. Quét ngược (LIFO: Mới nhất -> Cũ nhất)
	// Giúp lấy mã mới nhất vừa về, tránh lấy mã cũ rích
	for i := len(rows) - 1; i >= 0; i-- {
		// Safety Break: Không quét quá sâu (Tránh tốn CPU)
		if processCount >= RANGES.EMAIL_LIMIT_ROWS { break }
		processCount++
		
		row := rows[i]
		if len(row) <= 7 { continue } // Bỏ qua dòng lỗi/thiếu cột

		// Check Time
		mailTime := ConvertSerialDate(row[0]) 
		if mailTime < limitTime { break } // Mail quá cũ -> Dừng luôn vòng lặp

		// Check Logic Mail
		code := fmt.Sprintf("%v", row[6])
		if code == "" { continue } // Không có OTP -> Bỏ qua
		
		isRead := CleanString(row[7])
		if isRead == "true" { continue } // Đã đọc rồi -> Bỏ qua
		
		if CleanString(row[2]) != email { continue } // Sai email nhận -> Bỏ qua
		
		// Check Keyword (Lọc theo Sender/Subject)
		if keyword != "" && !strings.Contains(CleanString(row[3]), keyword) { continue }

		// -> TÌM THẤY!
		found = true
		targetIdx = i
		foundCode = code // Lưu code lại để đối chiếu
		
		resultData = map[string]interface{}{
			"date": row[0], "sender_name": row[1], "receiver_email": row[2],
			"sender_email": row[3], "subject": row[4], "body": row[5], "code": row[6],
		}
		break // Lấy được 1 cái mới nhất là dừng ngay
	}
	STATE.SheetMutex.RUnlock() // 🔓 Mở khóa ĐỌC

	// 4. Đánh dấu đã đọc (An toàn Đa luồng)
	if found && markRead {
		STATE.SheetMutex.Lock() // 🔒 Khóa GHI (Bắt đầu vùng nguy hiểm)
		
		// 🔥 DOUBLE CHECK (QUAN TRỌNG NHẤT FILE NÀY)
		// Lý do: Giữa lúc RUnlock (ở trên) và Lock (ở đây), luồng Cleanup có thể đã chạy và xóa bớt dòng.
		// Dẫn đến targetIdx không còn đúng là dòng mail đó nữa.
		// Giải pháp: Kiểm tra lại xem nội dung tại targetIdx có khớp với foundCode và email không.
		
		if targetIdx < len(cacheData.RawValues) {
			currentRow := cacheData.RawValues[targetIdx]
			
			// Lấy lại dữ liệu thực tế trong RAM lúc này
			currentCode := ""
			if len(currentRow) > 6 { currentCode = fmt.Sprintf("%v", currentRow[6]) }
			currentEmail := ""
			if len(currentRow) > 2 { currentEmail = CleanString(currentRow[2]) }
			
			// Chỉ update nếu dữ liệu KHỚP (Chứng tỏ dòng chưa bị dịch chuyển)
			if currentCode == foundCode && currentEmail == email {
				// Update RAM: Cột 7 (Index H) là cột "IsRead"
				if len(currentRow) <= 7 {
					// Nếu dòng ngắn quá, mở rộng ra để ghi
					newRow := make([]interface{}, 8)
					copy(newRow, currentRow)
					cacheData.RawValues[targetIdx] = newRow
				}
				cacheData.RawValues[targetIdx][7] = "TRUE"

				// Update CleanValues (cho Search)
				if targetIdx < len(cacheData.CleanValues) {
					if len(cacheData.CleanValues[targetIdx]) <= 7 {
						newClean := make([]string, 8)
						copy(newClean, cacheData.CleanValues[targetIdx])
						cacheData.CleanValues[targetIdx] = newClean
					}
					cacheData.CleanValues[targetIdx][7] = "true"
				}
				
				// Copy dữ liệu để ghi xuống Disk (Queue)
				rowToUpdate := make([]interface{}, len(cacheData.RawValues[targetIdx]))
				copy(rowToUpdate, cacheData.RawValues[targetIdx])
				
				STATE.SheetMutex.Unlock() // 🔓 Mở khóa GHI sớm (để Queue chạy bên ngoài)
				
				// Đẩy xuống Queue update
				QueueUpdate(sid, SHEET_NAMES.EMAIL_LOGGER, targetIdx, rowToUpdate)
			} else {
				// Dữ liệu đã bị trôi -> Bỏ qua việc update để an toàn (Thà user lấy lại lần sau còn hơn đánh dấu nhầm)
				STATE.SheetMutex.Unlock()
			}
		} else {
			// Index đã vượt quá mảng (do bị cắt bớt quá nhiều)
			STATE.SheetMutex.Unlock()
		}
	}

	// 5. Trả về kết quả
	if found {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "true", "messenger": "Lấy mã thành công", "email": resultData,
		})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "true", "messenger": "Không tìm thấy mail", "email": map[string]interface{}{},
		})
	}
}

// =================================================================================================
// 🟢 LOGIC DỌN DẸP AN TOÀN (FLUSH BEFORE DELETE)
// =================================================================================================

func CleanupOldMails(sid string) {
	// 🔥 BƯỚC 1: FLUSH QUEUE (QUAN TRỌNG)
	// Ghi hết dữ liệu "đang chờ" xuống đĩa trước khi xóa bất cứ thứ gì.
	// Việc này đảm bảo các lệnh "Đánh dấu đã đọc" không bị mất.
	// Hàm FlushQueue tự quản lý Lock của Queue, nên không cần Lock Sheet ở đây.
	FlushQueue(sid, false)

	// 🔥 BƯỚC 2: KHÓA VÀ CẮT RAM (Thao tác cực nhanh)
	STATE.SheetMutex.Lock()
	
	cacheKey := sid + KEY_SEPARATOR + SHEET_NAMES.EMAIL_LOGGER
	cached, exists := STATE.SheetCache[cacheKey]
	
	if !exists { 
		STATE.SheetMutex.Unlock(); return 
	}

	ramCount := len(cached.RawValues)
	// Ngưỡng kích hoạt: Max (1112) - Start (112) = 1000 dòng
	thresholdRAM := RANGES.MAX_ROW_CLEAN - RANGES.EMAIL_START_ROW

	// Kiểm tra lại lần nữa trong Lock (Double Check)
	if ramCount > thresholdRAM {
		log.Printf("🧹 [CLEANUP] Bắt đầu cắt RAM Email (Hiện tại: %d dòng)...", ramCount)

		if ramCount > RANGES.DELETE_COUNT {
			// Cắt bỏ phần đầu (Slicing) - Giữ lại phần đuôi
			cached.RawValues = cached.RawValues[RANGES.DELETE_COUNT:]
			cached.CleanValues = cached.CleanValues[RANGES.DELETE_COUNT:]
		} else {
			// Trường hợp xóa sạch
			cached.RawValues = [][]interface{}{}
			cached.CleanValues = [][]string{}
		}
		
		STATE.SheetMutex.Unlock() // 🔓 Mở khóa ngay lập tức (Server hoạt động lại bình thường)

		// 🔥 BƯỚC 3: XÓA TRÊN DISK (Chạy nền, gọi Google API)
		go func() {
			startIndex := int64(RANGES.EMAIL_START_ROW - 1)
			endIndex := startIndex + int64(RANGES.DELETE_COUNT)

			req := &sheets.BatchUpdateSpreadsheetRequest{
				Requests: []*sheets.Request{
					{
						DeleteDimension: &sheets.DeleteDimensionRequest{
							Range: &sheets.DimensionRange{
								SheetId:   getSheetIdByName(sid, SHEET_NAMES.EMAIL_LOGGER),
								Dimension: "ROWS",
								StartIndex: startIndex,
								EndIndex:   endIndex,
							},
						},
					},
				},
			}

			_, err := sheetsService.Spreadsheets.BatchUpdate(sid, req).Do()
			if err != nil {
				log.Printf("❌ [CLEANUP ERROR] Lỗi xóa Sheet: %v", err)
			} else {
				log.Println("✅ [CLEANUP SUCCESS] Hoàn tất dọn dẹp Google Sheet.")
			}
		}()
	} else {
		STATE.SheetMutex.Unlock()
	}
}

// Hàm hỗ trợ lấy Sheet ID
func getSheetIdByName(spreadsheetId, sheetName string) int64 {
	resp, err := sheetsService.Spreadsheets.Get(spreadsheetId).Do()
	if err != nil { return 0 }
	for _, sheet := range resp.Sheets {
		if sheet.Properties.Title == sheetName {
			return sheet.Properties.SheetId
		}
	}
	return 0
}
