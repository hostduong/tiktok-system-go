package sheets

import (
	"context"
	"fmt"
	
	"google.golang.org/api/sheets/v4"
    // Lưu ý: Không cần import "google.golang.org/api/option" vì ta dùng quyền mặc định của Server
)

type Service struct {
	srv *sheets.Service
}

// NewService: Khởi tạo kết nối Google Sheets
// 🔥 ĐIỂM QUAN TRỌNG: Hàm này KHÔNG nhận tham số credentials nữa.
// Nó sẽ tự động lấy "Căn Cước" của Cloud Run (My First Project) để đi làm việc.
func NewService() (*Service, error) {
	ctx := context.Background()
	
	// Tương đương Node.js: const auth = new google.auth.GoogleAuth(...)
	// Go sẽ tự tìm quyền của Server (ADC - Application Default Credentials)
	srv, err := sheets.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("lỗi khởi tạo Sheets Service (ADC): %v", err)
	}

	return &Service{srv: srv}, nil
}

// FetchData: Hàm đọc dữ liệu (Logic giữ nguyên)
func (s *Service) FetchData(spreadsheetID, sheetName string, startRow, endRow int) ([][]interface{}, error) {
	// Đọc từ cột A đến cột BI (giống Node.js LIMIT_COL_FULL: "BI")
	readRange := fmt.Sprintf("'%s'!A%d:BI%d", sheetName, startRow, endRow)
	
	resp, err := s.srv.Spreadsheets.Values.Get(spreadsheetID, readRange).ValueRenderOption("UNFORMATTED_VALUE").Do()
	if err != nil {
		return nil, err
	}
	return resp.Values, nil
}

// BatchUpdate: Hàm ghi dữ liệu (Logic giữ nguyên)
func (s *Service) BatchUpdate(spreadsheetID string, requests []*sheets.ValueRange) error {
	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             requests,
	}
	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}

// Append: Hàm thêm dòng mới (Logic giữ nguyên)
func (s *Service) Append(spreadsheetID, sheetName string, values [][]interface{}) error {
    rangeVal := fmt.Sprintf("'%s'!A1", sheetName)
    rb := &sheets.ValueRange{
        Values: values,
    }
    _, err := s.srv.Spreadsheets.Values.Append(spreadsheetID, rangeVal, rb).ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Do()
    return err
}
