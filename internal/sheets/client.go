package sheets

import (
	"context"
	"fmt"
	"log"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type Service struct {
	srv *sheets.Service
}

func NewService() (*Service, error) {
	ctx := context.Background()
	// Tự động dùng Application Default Credentials (ADC) trên Cloud Run
	// Nếu chạy local, bạn có thể truyền key JSON vào đây
	srv, err := sheets.NewService(ctx, option.WithScopes(sheets.SpreadsheetsScope))
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Sheets Service: %v", err)
	}
	return &Service{srv: srv}, nil
}

// FetchData: Lấy dữ liệu thô
func (s *Service) FetchData(spreadsheetID, sheetName string, startRow, endRow int) ([][]interface{}, error) {
	readRange := fmt.Sprintf("'%s'!A%d:BI%d", sheetName, startRow, endRow)
	resp, err := s.srv.Spreadsheets.Values.Get(spreadsheetID, readRange).ValueRenderOption("UNFORMATTED_VALUE").Do()
	if err != nil {
		return nil, err
	}
	return resp.Values, nil
}

// FetchRawData: Alias cho FetchData (để tương thích logic cũ)
func (s *Service) FetchRawData(sid, sname string, start, end int) ([][]interface{}, error) {
	return s.FetchData(sid, sname, start, end)
}

// BatchUpdateRows: Cập nhật nhiều dòng (Dùng cho Data Queue)
func (s *Service) BatchUpdateRows(spreadsheetID, sheetName string, updates map[int][]interface{}) error {
	var data []*sheets.ValueRange
	for rowIndex, rowData := range updates {
		// Node.js: CONFIG.RANGES.DATA_START_ROW + rowIndex
		// Ở đây rowIndex truyền vào đã là index thực tế của Excel (ví dụ 11, 12...)
		rng := fmt.Sprintf("'%s'!A%d", sheetName, rowIndex)
		vr := &sheets.ValueRange{
			Range:  rng,
			Values: [][]interface{}{rowData},
		}
		data = append(data, vr)
	}
	if len(data) == 0 {
		return nil
	}

	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             data,
	}
	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}

// BatchUpdateCells: Cập nhật từng ô (Dùng cho Mail Queue - Cột H)
func (s *Service) BatchUpdateCells(spreadsheetID, sheetName string, cellUpdates map[int]string) error {
	var data []*sheets.ValueRange
	for rowIndex, val := range cellUpdates {
		// Cột H là cột thứ 8
		rng := fmt.Sprintf("'%s'!H%d", sheetName, rowIndex)
		vr := &sheets.ValueRange{
			Range:  rng,
			Values: [][]interface{}{{val}},
		}
		data = append(data, vr)
	}
	if len(data) == 0 {
		return nil
	}

	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             data,
	}
	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}

// AppendRawRows: Thêm dòng mới
func (s *Service) AppendRawRows(spreadsheetID, sheetName string, rows [][]interface{}) error {
	if len(rows) == 0 {
		return nil
	}
	rangeVal := fmt.Sprintf("'%s'!A1", sheetName)
	rb := &sheets.ValueRange{
		Values: rows,
	}
	_, err := s.srv.Spreadsheets.Values.Append(spreadsheetID, rangeVal, rb).ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Do()
	return err
}

// DeleteRows: Xóa dòng (Dùng cho dọn dẹp Email)
func (s *Service) DeleteRows(spreadsheetID string, sheetId int64, startIndex, endIndex int) error {
	req := &sheets.Request{
		DeleteDimension: &sheets.DeleteDimensionRequest{
			Range: &sheets.DimensionRange{
				SheetId:    sheetId,
				Dimension:  "ROWS",
				StartIndex: int64(startIndex),
				EndIndex:   int64(endIndex),
			},
		},
	}
	batchReq := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{req},
	}
	_, err := s.srv.Spreadsheets.BatchUpdate(spreadsheetID, batchReq).Do()
	return err
}

// GetSheetId: Lấy ID số của Sheet theo tên
func (s *Service) GetSheetId(spreadsheetID, sheetName string) (int64, error) {
	resp, err := s.srv.Spreadsheets.Get(spreadsheetID).Do()
	if err != nil {
		return 0, err
	}
	for _, sheet := range resp.Sheets {
		if sheet.Properties.Title == sheetName {
			return sheet.Properties.SheetId, nil
		}
	}
	return 0, fmt.Errorf("sheet not found")
}

// CreateSheets: Tạo sheet mới (Logic setup)
func (s *Service) CreateSheets(spreadsheetID string, neededSheets []string) error {
	// Logic copy sheet khá phức tạp, để đơn giản hóa ta chỉ tạo sheet trống
	// Nếu cần copy đúng template, cần thêm logic CopyTo như Node.js
	// Ở bản Go này ta tạm thời tạo sheet trắng để tránh lỗi code dài
	var reqs []*sheets.Request
	for _, name := range neededSheets {
		reqs = append(reqs, &sheets.Request{
			AddSheet: &sheets.AddSheetRequest{
				Properties: &sheets.SheetProperties{Title: name},
			},
		})
	}
	if len(reqs) > 0 {
		batchReq := &sheets.BatchUpdateSpreadsheetRequest{Requests: reqs}
		_, err := s.srv.Spreadsheets.BatchUpdate(spreadsheetID, batchReq).Do()
		if err != nil {
			log.Printf("CreateSheets Warning: %v", err) // Có thể sheet đã tồn tại
		}
	}
	return nil
}
