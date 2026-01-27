package sheets

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"

	"tiktok-server/internal/models"
)

type Service struct {
	srv *sheets.Service
}

func NewService() (*Service, error) {
	ctx := context.Background()

	// 1. Lấy Key từ biến môi trường (Dùng chung Key với Firebase)
	credJSON := os.Getenv("FIREBASE_CREDENTIALS")
	if credJSON == "" {
		return nil, fmt.Errorf("biến môi trường FIREBASE_CREDENTIALS đang rỗng")
	}

	// 2. Kết nối Sheets API bằng Key JSON đó
	srv, err := sheets.NewService(ctx, option.WithCredentialsJSON([]byte(credJSON)))
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve Sheets client: %v", err)
	}

	return &Service{srv: srv}, nil
}

// FetchData: Lấy dữ liệu và map vào Struct (Dùng cho Login/DataTiktok)
func (s *Service) FetchData(spreadsheetID, sheetName string, startRow, endRow int) ([]*models.TikTokAccount, error) {
	readRange := fmt.Sprintf("%s!A%d:BI%d", sheetName, startRow, endRow) // BI là cột 61

	resp, err := s.srv.Spreadsheets.Values.Get(spreadsheetID, readRange).Do()
	if err != nil {
		return nil, err
	}

	var accounts []*models.TikTokAccount
	for i, row := range resp.Values {
		acc := models.NewAccount()
		// Chuyển row ( []interface{} ) thành []string
		strRow := make([]string, 61)
		for j := 0; j < 61; j++ {
			if j < len(row) {
				strRow[j] = fmt.Sprintf("%v", row[j])
			} else {
				strRow[j] = ""
			}
		}
		acc.FromSlice(strRow)
		acc.RowIndex = startRow + i // Lưu lại dòng thực tế
		accounts = append(accounts, acc)
	}

	return accounts, nil
}

// FetchRawData: Lấy dữ liệu thô (Dùng cho EmailLogger - Mail)
func (s *Service) FetchRawData(spreadsheetID, sheetName string, startRow, endRow int) ([][]interface{}, error) {
	readRange := fmt.Sprintf("%s!A%d:H%d", sheetName, startRow, endRow) // Lấy đến cột H

	resp, err := s.srv.Spreadsheets.Values.Get(spreadsheetID, readRange).Do()
	if err != nil {
		return nil, fmt.Errorf("google api error: %v", err)
	}

	return resp.Values, nil
}

// BatchUpdateRows: Cập nhật nhiều dòng cùng lúc (Dùng cho DataTiktok)
func (s *Service) BatchUpdateRows(spreadsheetID, sheetName string, updates map[int]*models.TikTokAccount) error {
	if len(updates) == 0 {
		return nil
	}

	var data []*sheets.ValueRange
	for rowIndex, acc := range updates {
		rng := fmt.Sprintf("%s!A%d", sheetName, rowIndex)
		values := []interface{}{}
		strSlice := acc.ToSlice()
		for _, v := range strSlice {
			values = append(values, v)
		}

		data = append(data, &sheets.ValueRange{
			Range:  rng,
			Values: [][]interface{}{values},
		})
	}

	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             data,
	}

	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}

// AppendRawRows: Thêm dòng mới (Dùng cho Log/Append)
func (s *Service) AppendRawRows(spreadsheetID, sheetName string, rows [][]interface{}) error {
	rng := fmt.Sprintf("%s!A1", sheetName)
	rb := &sheets.ValueRange{
		Values: rows,
	}

	_, err := s.srv.Spreadsheets.Values.Append(spreadsheetID, rng, rb).
		ValueInputOption("RAW").
		InsertDataOption("INSERT_ROWS").
		Do()
	return err
}

// BatchUpdateCells: Cập nhật từng ô lẻ (Dùng cho Mail - đánh dấu đã đọc)
func (s *Service) BatchUpdateCells(spreadsheetID, sheetName string, updates map[int]string) error {
	if len(updates) == 0 {
		return nil
	}

	var data []*sheets.ValueRange
	for rowIndex, val := range updates {
		// Update cột H (Cột 7)
		rng := fmt.Sprintf("%s!H%d", sheetName, rowIndex)
		data = append(data, &sheets.ValueRange{
			Range:  rng,
			Values: [][]interface{}{{val}},
		})
	}

	rb := &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             data,
	}

	_, err := s.srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, rb).Do()
	return err
}
