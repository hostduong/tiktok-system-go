package sheets

import (
	"context"
	"fmt"

	"google.golang.org/api/sheets/v4"
	"tiktok-server/internal/models"
)

type Service struct {
	srv *sheets.Service
}

// NewService: Khởi tạo kết nối dùng quyền Server Cloud Run
func NewService() (*Service, error) {
	ctx := context.Background()
	srv, err := sheets.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Sheets Service: %v", err)
	}
	return &Service{srv: srv}, nil
}

// FetchData: Đọc dữ liệu từ Sheet
func (s *Service) FetchData(spreadsheetID, sheetName string, startRow, endRow int) ([][]interface{}, error) {
	readRange := fmt.Sprintf("'%s'!A%d:BI%d", sheetName, startRow, endRow)
	resp, err := s.srv.Spreadsheets.Values.Get(spreadsheetID, readRange).ValueRenderOption("UNFORMATTED_VALUE").Do()
	if err != nil {
		return nil, err
	}
	return resp.Values, nil
}

// BatchUpdateRows: Cập nhật nhiều dòng dựa trên map account
// Khớp với lỗi: (string, string, map[int]*models.TikTokAccount)
func (s *Service) BatchUpdateRows(spreadsheetID string, sheetName string, updates map[int]*models.TikTokAccount) error {
	var vr []*sheets.ValueRange

	for rowIndex, acc := range updates {
		// 11 là dòng bắt đầu dữ liệu trong CONFIG.RANGES.DATA_START_ROW
		// rowIndex trong Go thường bắt đầu từ 0, nên dòng thực tế trên Sheet là rowIndex + 11
		excelRow := rowIndex + 11 
		
		// Chuyển đổi struct Account thành mảng []interface{} để ghi vào Sheet
		rowValues := acc.ToRow() 

		vr = append(vr, &sheets.ValueRange{
			Range:  fmt.Sprintf("'%s'!A%d", sheetName, excelRow),
			Values: [][]interface{}{rowValues},
		})
	}

	if len(vr) == 0 {
		return nil
	}

	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             vr,
	}
	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}

// AppendRawRows: Thêm dòng mới
func (s *Service) AppendRawRows(spreadsheetID, sheetName string, values [][]interface{}) error {
	rangeVal := fmt.Sprintf("'%s'!A1", sheetName)
	rb := &sheets.ValueRange{
		Values: values,
	}
	_, err := s.srv.Spreadsheets.Values.Append(spreadsheetID, rangeVal, rb).ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Do()
	return err
}

// BatchUpdateCells: Cập nhật một cột cụ thể (thường là cột Trạng thái/Mail)
// Khớp với lỗi: (string, string, map[int]string)
func (s *Service) BatchUpdateCells(spreadsheetID string, sheetName string, updates map[int]string) error {
	var vr []*sheets.ValueRange

	for rowIndex, value := range updates {
		// Tùy vào logic worker, thường là cập nhật cột H (index 7) cho Mail hoặc cột A cho Status
		// Ở đây tôi dùng cột H (Email Read Status) theo mẫu Node.js cũ của bạn
		excelRow := rowIndex 
		
		vr = append(vr, &sheets.ValueRange{
			Range:  fmt.Sprintf("'%s'!H%d", sheetName, excelRow),
			Values: [][]interface{}{{value}},
		})
	}

	if len(vr) == 0 {
		return nil
	}

	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             vr,
	}
	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}
