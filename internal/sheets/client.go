package sheets

import (
	"context"
	"fmt"
	"os"

	"tiktok-server/internal/models"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Service đóng gói Google Sheets API Client
type Service struct {
	srv *sheets.Service
}

// NewService khởi tạo kết nối Google Sheets
// Nó sẽ đọc biến môi trường FIREBASE_CREDENTIALS (chứa JSON key)
func NewService() (*Service, error) {
	ctx := context.Background()
	
	// Lấy JSON Key từ biến môi trường (Giống Node.js process.env.FIREBASE_CREDENTIALS)
	credsJSON := os.Getenv("FIREBASE_CREDENTIALS")
	if credsJSON == "" {
		return nil, fmt.Errorf("missing env var: FIREBASE_CREDENTIALS")
	}

	// Tạo Client Google
	srv, err := sheets.NewService(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return nil, fmt.Errorf("failed to create sheets service: %v", err)
	}

	return &Service{srv: srv}, nil
}

// FetchData: Đọc dữ liệu từ Sheet và chuyển đổi sang Struct
func (s *Service) FetchData(spreadsheetID, sheetName string, startRow, endRow int) ([]*models.TikTokAccount, error) {
	// 1. Định nghĩa vùng đọc (Ví dụ: DataTiktok!A11:BI10000)
	// Cột BI là cột thứ 61
	readRange := fmt.Sprintf("%s!A%d:BI%d", sheetName, startRow, endRow)

	// 2. Gọi API Google (Đọc thô)
	resp, err := s.srv.Spreadsheets.Values.Get(spreadsheetID, readRange).Do()
	if err != nil {
		return nil, fmt.Errorf("google api error: %v", err)
	}

	// 3. Chuyển đổi dữ liệu (Mapping)
	var accounts []*models.TikTokAccount
	
	// Nếu không có dữ liệu, trả về mảng rỗng
	if len(resp.Values) == 0 {
		return accounts, nil
	}

	for i, row := range resp.Values {
		// Tạo account mới
		acc := models.NewAccount()
		
		// Map dữ liệu thô vào Struct (Logic nằm bên models/account.go)
		acc.FromSlice(row)
		
		// Gán RowIndex thực tế (Dùng để update sau này)
		acc.RowIndex = startRow + i
		
		accounts = append(accounts, acc)
	}

	return accounts, nil
}

// BatchUpdateRows: Cập nhật nhiều dòng cùng lúc (Tối ưu API call)
// Input: Map[RowIndex] -> AccountData
func (s *Service) BatchUpdateRows(spreadsheetID, sheetName string, updates map[int]*models.TikTokAccount) error {
	if len(updates) == 0 {
		return nil
	}

	var data []*sheets.ValueRange

	// Duyệt qua danh sách cần update
	for rowIndex, acc := range updates {
		// Chuyển Struct thành Mảng thô
		rawRow := acc.ToSlice()

		// Tạo vùng ghi cho dòng đó (Ví dụ: A15:BI15)
		rng := fmt.Sprintf("%s!A%d:BI%d", sheetName, rowIndex, rowIndex)

		data = append(data, &sheets.ValueRange{
			Range:  rng,
			Values: [][]interface{}{rawRow},
		})
	}

	// Gọi API batchUpdate (1 request sửa được nhiều dòng)
	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW", // Ghi nguyên văn, không tự format
		Data:             data,
	}

	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	if err != nil {
		return fmt.Errorf("batch update error: %v", err)
	}

	return nil
}

// AppendRows: Thêm dòng mới vào cuối Sheet
func (s *Service) AppendRows(spreadsheetID, sheetName string, newAccounts []*models.TikTokAccount) error {
	if len(newAccounts) == 0 {
		return nil
	}

	var rawValues [][]interface{}
	for _, acc := range newAccounts {
		rawValues = append(rawValues, acc.ToSlice())
	}

	// Vùng ghi: Bắt đầu từ A1, Google sẽ tự tìm dòng trống cuối cùng
	rng := fmt.Sprintf("%s!A1", sheetName)

	rb := &sheets.ValueRange{
		Values: rawValues,
	}

	// Gọi API Append
	_, err := s.srv.Spreadsheets.Values.Append(spreadsheetID, rng, rb).
		ValueInputOption("RAW").
		InsertDataOption("INSERT_ROWS"). // Quan trọng: Chèn dòng mới
		Do()

	if err != nil {
		return fmt.Errorf("append error: %v", err)
	}

	return nil
}
