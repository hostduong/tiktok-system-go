package sheets

import (
	"context"
	"fmt"
	
	"google.golang.org/api/sheets/v4"
)

type Service struct {
	srv *sheets.Service
}

// NewService: Khởi tạo kết nối (Dùng quyền Server - ADC)
func NewService() (*Service, error) {
	ctx := context.Background()
	srv, err := sheets.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Sheets Service (ADC): %v", err)
	}
	return &Service{srv: srv}, nil
}

// FetchData: Đọc dữ liệu
func (s *Service) FetchData(spreadsheetID, sheetName string, startRow, endRow int) ([][]interface{}, error) {
	readRange := fmt.Sprintf("'%s'!A%d:BI%d", sheetName, startRow, endRow)
	resp, err := s.srv.Spreadsheets.Values.Get(spreadsheetID, readRange).ValueRenderOption("UNFORMATTED_VALUE").Do()
	if err != nil {
		return nil, err
	}
	return resp.Values, nil
}

// ---------------------------------------------------------
// 🔥 CÁC HÀM DƯỚI ĐÂY ĐƯỢC ĐỔI TÊN ĐỂ KHỚP VỚI worker.go
// ---------------------------------------------------------

// BatchUpdateRows: Cập nhật nhiều dòng (Tương ứng với queue_update)
func (s *Service) BatchUpdateRows(spreadsheetID string, requests []*sheets.ValueRange) error {
	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             requests,
	}
	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}

// AppendRawRows: Thêm dòng mới (Tương ứng với queue_append)
func (s *Service) AppendRawRows(spreadsheetID, sheetName string, values [][]interface{}) error {
	rangeVal := fmt.Sprintf("'%s'!A1", sheetName)
	rb := &sheets.ValueRange{
		Values: values,
	}
	_, err := s.srv.Spreadsheets.Values.Append(spreadsheetID, rangeVal, rb).ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Do()
	return err
}

// BatchUpdateCells: Cập nhật ô (Tương ứng với logic đánh dấu mail đã đọc)
// Trong logic Node.js cũ, cái này cũng dùng values.batchUpdate giống BatchUpdateRows
func (s *Service) BatchUpdateCells(spreadsheetID string, requests []*sheets.ValueRange) error {
	// Tái sử dụng logic của BatchUpdateRows
	return s.BatchUpdateRows(spreadsheetID, requests)
}
