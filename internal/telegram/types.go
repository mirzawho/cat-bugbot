package telegram

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Message struct {
	MessageID int64   `json:"message_id"`
	From      *User   `json:"from,omitempty"`
	Chat      Chat    `json:"chat"`
	Date      int64   `json:"date"`
	Text      string  `json:"text,omitempty"`
	Photo     []Photo `json:"photo,omitempty"`
	Video     *Video  `json:"video,omitempty"`
}

type Photo struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int    `json:"file_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type Video struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int    `json:"file_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Duration     int    `json:"duration"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	Photo     *Photo `json:"photo,omitempty"`
}

func (u User) DisplayName() string {
	switch {
	case u.FirstName != "" && u.LastName != "":
		return u.FirstName + " " + u.LastName
	case u.FirstName != "":
		return u.FirstName
	default:
		return u.Username
	}
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data"`
}

type InlineKeyboardButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// KeyboardButton is a bottom (reply) keyboard button; WebApp opens a Mini App.
type KeyboardButton struct {
	Text   string      `json:"text"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

// ReplyKeyboardMarkup is the persistent keyboard under the message box.
type ReplyKeyboardMarkup struct {
	Keyboard       [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard bool               `json:"resize_keyboard,omitempty"`
	IsPersistent   bool               `json:"is_persistent,omitempty"`
}

func Rows(rows ...[]InlineKeyboardButton) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: rows}
}

func Button(text, data string) []InlineKeyboardButton {
	return []InlineKeyboardButton{{Text: text, CallbackData: data}}
}
