package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// SendMedia uploads a photo or video. Telegram returns fresh file IDs that are
// valid for this bot, which is how media received by some other bot (an old
// token) becomes usable again.
func (c *Client) SendMedia(ctx context.Context, chatID int64, mediaType, filename string, data []byte, caption string) (Message, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", fmt.Sprint(chatID))
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	field := "photo"
	if mediaType == "video" {
		field = "video"
	}
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		return Message{}, err
	}
	if _, err := part.Write(data); err != nil {
		return Message{}, err
	}
	if err := w.Close(); err != nil {
		return Message{}, err
	}

	method := "sendPhoto"
	if mediaType == "video" {
		method = "sendVideo"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, &body)
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Message{}, err
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return Message{}, fmt.Errorf("telegram %s: http %d", method, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || !ar.OK {
		return Message{}, &Error{Method: method, Code: ar.ErrorCode, Description: ar.Description}
	}
	var msg Message
	if err := json.Unmarshal(ar.Result, &msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// FileFromMessage returns the media a sent message carries, with the file IDs
// the current bot knows.
func (c *Client) FileFromMessage(m Message) (typeName, fileID, uniqueID string, ok bool) {
	if len(m.Photo) > 0 {
		p := m.Photo[len(m.Photo)-1]
		return "image", p.FileID, p.FileUniqueID, true
	}
	if m.Video != nil {
		return "video", m.Video.FileID, m.Video.FileUniqueID, true
	}
	return "", "", "", false
}
