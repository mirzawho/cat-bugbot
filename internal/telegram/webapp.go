package telegram

// WebAppInfo opens a Telegram Mini App (https URL) from a button.
type WebAppInfo struct {
	URL string `json:"url"`
}

// WebAppButton is an inline button that launches a Mini App.
func WebAppButton(text, url string) []InlineKeyboardButton {
	return []InlineKeyboardButton{{Text: text, WebApp: &WebAppInfo{URL: url}}}
}
