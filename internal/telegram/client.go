package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type API interface {
	GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error)
	SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboardMarkup) (int64, error)
	SendMessageWithReplyKeyboard(ctx context.Context, chatID int64, text string, kb *ReplyKeyboardMarkup) (int64, error)
	EditMessageText(ctx context.Context, chatID, messageID int64, text string, kb *InlineKeyboardMarkup) error
	DeleteMessage(ctx context.Context, chatID, messageID int64) error
	AnswerCallback(ctx context.Context, id, text string) error
	GetFile(ctx context.Context, fileID string) (File, error)
	DownloadFile(ctx context.Context, filePath string) ([]byte, error)
	// SendMedia re-uploads a file to the chat and returns the new message.
	SendMedia(ctx context.Context, chatID int64, mediaType, filename string, data []byte, caption string) (Message, error)
	// FileFromMessage reports the media a sent message carries.
	FileFromMessage(m Message) (typeName, fileID, uniqueID string, ok bool)
	// SendNotification posts with arbitrary sendMessage options (e.g. reply anchoring).
	SendNotification(ctx context.Context, params map[string]any) error
	// GetChat returns a Telegram user's current profile.
	GetChat(ctx context.Context, userID int64) (User, error)
	// GetUserProfilePhotos returns the user's photo file_ids, best first.
	GetUserProfilePhotos(ctx context.Context, userID int64) ([]string, error)
}

const maxDownload = 25 << 20

type File struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}

type Client struct {
	base     string
	fileBase string
	http     *http.Client
}

func NewClient(token string) *Client {
	return NewClientWithBase("https://api.telegram.org", token)
}

func NewClientWithBase(baseURL, token string) *Client {
	return &Client{
		base:     baseURL + "/bot" + token + "/",
		fileBase: baseURL + "/file/bot" + token + "/",
		http:     &http.Client{Timeout: 90 * time.Second},
	}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
}

// ErrFileIDInvalid means the Bot API does not know this file_id: file IDs are
// scoped to the bot (and its data centre) that received them, so media stored
// by another bot or an old database can never be fetched by this one.
var ErrFileIDInvalid = errors.New("telegram: file_id is not valid for this bot")

// Error is a non-OK response from the Bot API. The flow inspects it to tell
// "nothing to change" and "this message can no longer be edited" apart.
type Error struct {
	Method      string
	Code        int
	Description string
}

func (e *Error) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// IsFileIDInvalid reports FILE_ID_INVALID / invalid file_id responses.
func (e *Error) IsFileIDInvalid() bool {
	return strings.Contains(e.Description, "FILE_ID_INVALID") ||
		strings.Contains(strings.ToLower(e.Description), "invalid file_id")
}

func (e *Error) IsNotModified() bool {
	return strings.Contains(e.Description, "message is not modified")
}

// IsNotEditable reports errors that mean the target message cannot be changed
// anymore (deleted, too old, no caption, or a media group).
func (e *Error) IsNotEditable() bool {
	switch {
	case strings.Contains(e.Description, "message to edit not found"),
		strings.Contains(e.Description, "message can't be edited"),
		strings.Contains(e.Description, "message_cant_be_edited"),
		strings.Contains(e.Description, "there is no caption"):
		return true
	}
	return false
}

func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return fmt.Errorf("telegram %s: http %d", method, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || !ar.OK {
		return &Error{Method: method, Code: ar.ErrorCode, Description: ar.Description}
	}
	if out != nil {
		return json.Unmarshal(ar.Result, out)
	}
	return nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	payload := map[string]any{
		"offset":          offset,
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}
	var updates []Update
	err := c.call(ctx, "getUpdates", payload, &updates)
	return updates, err
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboardMarkup) (int64, error) {
	payload := map[string]any{"chat_id": chatID, "text": text}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	var msg Message
	if err := c.call(ctx, "sendMessage", payload, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

func (c *Client) SendMessageWithReplyKeyboard(ctx context.Context, chatID int64, text string, kb *ReplyKeyboardMarkup) (int64, error) {
	payload := map[string]any{"chat_id": chatID, "text": text}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	var msg Message
	if err := c.call(ctx, "sendMessage", payload, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text string, kb *InlineKeyboardMarkup) error {
	payload := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	return c.call(ctx, "editMessageText", payload, nil)
}

func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	return c.call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, nil)
}

// BotCommand is one entry of the chat command menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetMyCommands registers the command menu (optionally for a scope).
func (c *Client) SetMyCommands(ctx context.Context, payload any) error {
	return c.call(ctx, "setMyCommands", payload, nil)
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}

func (c *Client) SendNotification(ctx context.Context, params map[string]any) error {
	return c.call(ctx, "sendMessage", params, nil)
}

func (c *Client) GetChat(ctx context.Context, userID int64) (User, error) {
	var u User
	err := c.call(ctx, "getChat", map[string]any{"chat_id": userID}, &u)
	return u, err
}

func (c *Client) GetUserProfilePhotos(ctx context.Context, userID int64) ([]string, error) {
	var res struct {
		TotalCount int       `json:"total_count"`
		Photos     [][]Photo `json:"photos"`
	}
	if err := c.call(ctx, "getUserProfilePhotos", map[string]any{"user_id": userID, "limit": 1}, &res); err != nil {
		return nil, err
	}
	if len(res.Photos) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(res.Photos[0]))
	for _, p := range res.Photos[0] {
		out = append(out, p.FileID)
	}
	return out, nil
}

func (c *Client) GetFile(ctx context.Context, fileID string) (File, error) {
	var f File
	err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.IsFileIDInvalid() {
		return File{}, ErrFileIDInvalid
	}
	return f, err
}

// DownloadFile fetches a file from the Bot API file endpoint.
func (c *Client) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	url := c.fileBase + filePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram file: http %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) >= maxDownload {
		return nil, fmt.Errorf("telegram file: too large")
	}
	return raw, nil
}

func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	payload := map[string]any{"callback_query_id": id}
	if text != "" {
		payload["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", payload, nil)
}
