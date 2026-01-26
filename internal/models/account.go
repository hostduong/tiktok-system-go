package models

// TikTokAccount đại diện cho 1 dòng dữ liệu (tiết kiệm RAM tối đa)
type TikTokAccount struct {
	// Metadata (Không lưu trong Sheet, chỉ dùng để quản lý vị trí)
	RowIndex int `json:"row_index"`

	// Dữ liệu chính (Mapping theo thứ tự cột)
	Status          string `json:"status"`           // Cột 0
	Note            string `json:"note"`             // Cột 1
	DeviceId        string `json:"device_id"`        // Cột 2
	UserId          string `json:"user_id"`          // Cột 3
	UserSec         string `json:"user_sec"`         // Cột 4
	UserName        string `json:"user_name"`        // Cột 5
	Email           string `json:"email"`            // Cột 6
	NickName        string `json:"nick_name"`        // Cột 7
	Password        string `json:"password"`         // Cột 8
	PasswordEmail   string `json:"password_email"`   // Cột 9
	RecoveryEmail   string `json:"recovery_email"`   // Cột 10
	TwoFA           string `json:"two_fa"`           // Cột 11
	Phone           string `json:"phone"`            // Cột 12

	// Các cột còn lại (13 -> 60) gom vào mảng để tiết kiệm code định nghĩa
	// Nếu sau này cần dùng cột nào cụ thể, ta sẽ lôi nó ra thành field riêng
	ExtraData []string `json:"-"` 
}

// Hàm tạo nhanh một Account rỗng
func NewAccount() *TikTokAccount {
	return &TikTokAccount{
		ExtraData: make([]string, 48), // 61 cột tổng - 13 cột đã khai báo
	}
}
