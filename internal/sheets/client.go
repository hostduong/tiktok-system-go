package sheets

import (
	"context"
	"fmt"
	
	"google.golang.org/api/sheets/v4"
)

type Service struct {
	srv *sheets.Service
}

// NewService: Khởi tạo kết nối Google Sheets
// 🔥 KHÔNG CẦN TRUYỀN KEY JSON. Tự động dùng quyền của Server (Cloud Run)
func NewService() (*Service, error) {
	ctx := context.Background()
	
	// Tự động tìm "Application Default Credentials" của Server
	srv, err := sheets.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Sheets Service (ADC): %v", err)
	}

	return &Service{srv: srv}, nil
}

// FetchData: Đọc dữ liệu từ Sheet
func (s *Service) FetchData(spreadsheetID, sheetName string, startRow, endRow int) ([][]interface{}, error) {
	// Đọc từ cột A đến cột BI (Limit Col Full)
	readRange := fmt.Sprintf("'%s'!A%d:BI%d", sheetName, startRow, endRow)
	
	resp, err := s.srv.Spreadsheets.Values.Get(spreadsheetID, readRange).ValueRenderOption("UNFORMATTED_VALUE").Do()
	if err != nil {
		return nil, err
	}
	return resp.Values, nil
}

// BatchUpdate: Cập nhật nhiều dòng
func (s *Service) BatchUpdate(spreadsheetID string, requests []*sheets.ValueRange) error {
	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             requests,
	}
	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}

// Append: Thêm dòng mới
func (s *Service) Append(spreadsheetID, sheetName string, values [][]interface{}) error {
	rangeVal := fmt.Sprintf("'%s'!A1", sheetName)
	rb := &sheets.ValueRange{
		Values: values,
	}
	_, err := s.srv.Spreadsheets.Values.Append(spreadsheetID, rangeVal, rb).ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Do()
	return err
}
