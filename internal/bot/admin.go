package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	"bugbot/internal/domain"
	"bugbot/internal/telegram"
)

// NotifyResolved tells the reporter their report was accepted and resolved.
func (h *Handler) NotifyResolved(ctx context.Context, reportID string) error {
	r, err := h.db.Report(ctx, reportID)
	if err != nil {
		return err
	}
	_, err = h.send(ctx, r.User.ID, fmt.Sprintf(textReportResolved, r.Category), nil)
	return err
}

// NotifyRejected tells the reporter their report was not accepted.
func (h *Handler) NotifyRejected(ctx context.Context, reportID string) error {
	r, err := h.db.Report(ctx, reportID)
	if err != nil {
		return err
	}
	_, err = h.send(ctx, r.User.ID, fmt.Sprintf(textReportRejected, r.Category), nil)
	return err
}

// SendChat delivers an admin message to the reporter (web panel entry point).
func (h *Handler) SendChat(ctx context.Context, reportID, adminName, text string) (domain.ChatMessage, error) {
	r, err := h.db.Report(ctx, reportID)
	if err != nil {
		return domain.ChatMessage{}, err
	}
	return h.SendChatTo(ctx, reportID, h.db.AdminChatID(ctx, r.User.ID), adminName, text)
}

// SendChatTo delivers an admin message to a known chat. The reporter's first
// admin message opens the conversation: their next messages come to the team
// without any command, and the reply is a single, quiet chat message.
func (h *Handler) SendChatTo(ctx context.Context, reportID string, chatID int64, adminName, text string) (domain.ChatMessage, error) {
	r, err := h.db.Report(ctx, reportID)
	if err != nil {
		return domain.ChatMessage{}, err
	}
	if chatID == 0 {
		chatID = r.User.ID
	}
	msg, err := h.db.AppendChat(ctx, domain.ChatMessage{
		ReportID:  reportID,
		Sender:    domain.SenderAdmin,
		AdminName: adminName,
		Text:      text,
	})
	if err != nil {
		return domain.ChatMessage{}, err
	}

	running, err := h.db.OpenChat(ctx, r)
	if err != nil {
		return msg, err
	}
	// A user in the middle of a report keeps their flow; the reply still arrives.
	// Everyone else is switched into the conversation.
	if !running {
		if err := h.db.LinkChat(ctx, r.User.ID, chatID, reportID); err != nil {
			return msg, err
		}
	}

	// Tell the reporter which report the reply is about, and attach it to their
	// original message so the photo/caption is right there. A reply to that media
	// falls back to the plain message when Telegram refuses to link them.
	card := fmt.Sprintf(textAdminSays, r.Category, text)
	params := map[string]any{"chat_id": chatID, "text": card}
	if ref := h.db.ReporterMessage(ctx, reportID); ref.ChatID != 0 && ref.ChatID == chatID && ref.MessageID != 0 {
		params["reply_to_message_id"] = ref.MessageID
		params["allow_sending_without_reply"] = true
	}
	if err := h.api.SendNotification(ctx, params); err != nil && params["reply_to_message_id"] != nil {
		log.Printf("reply-to anchor for report %s failed (%v); sending a plain message", reportID, err)
		delete(params, "reply_to_message_id")
		delete(params, "allow_sending_without_reply")
		err = h.api.SendNotification(ctx, params)
		if err != nil {
			return msg, err
		}
	} else if err != nil {
		return msg, err
	}
	return msg, nil
}

func kbCancelChat() *telegram.InlineKeyboardMarkup {
	return telegram.Rows(telegram.Button("پایان گفت‌وگو", cbCancel))
}

// isPanelUser reports whether the user is an admin who may open the Mini App.
func (h *Handler) isPanelUser(id int64) bool {
	return h.panelURL != "" && h.isAdminFn != nil && h.isAdminFn(id)
}

// notifyAdmins tells every admin that a report came in, with a button that
// opens the panel right inside Telegram. The submitter is skipped, and a failed
// notice (e.g. an admin who never started the bot) must not fail the report.
func (h *Handler) notifyAdmins(ctx context.Context, r domain.Report) {
	if len(h.adminIDs) == 0 {
		return
	}
	desc := strings.TrimSpace(r.Description)
	if desc == "" {
		desc = textNoDescription
	}
	text := fmt.Sprintf(textNewReportNotice, r.User.DisplayName(), r.Category, desc, r.ID)

	var kb *telegram.InlineKeyboardMarkup
	if strings.HasPrefix(h.panelURL, "https://") {
		kb = telegram.Rows(telegram.WebAppButton(textAdminPanelButton, h.panelURL))
	}
	for _, id := range h.adminIDs {
		if id == r.User.ID {
			continue
		}
		params := map[string]any{"chat_id": id, "text": text}
		if kb != nil {
			params["reply_markup"] = kb
		}
		if err := h.api.SendNotification(ctx, params); err != nil {
			log.Printf("new report %s: notify admin %d: %v", r.ID, id, err)
		}
	}
}

// NewReportRequest is the persistent bottom keyboard button that starts a report.
const newReportRequest = "📝 ثبت گزارش باگ"

// adminMenu is the bottom keyboard for admins: panel Mini App on top (when the
// deployment is HTTPS), then a button that starts the report flow.
func adminMenu(panelURL string) *telegram.ReplyKeyboardMarkup {
	kb := &telegram.ReplyKeyboardMarkup{ResizeKeyboard: true, IsPersistent: true}
	if panelURL != "" && strings.HasPrefix(panelURL, "https://") {
		kb.Keyboard = append(kb.Keyboard, []telegram.KeyboardButton{
			{Text: textAdminPanelButton, WebApp: &telegram.WebAppInfo{URL: panelURL}},
		})
	}
	kb.Keyboard = append(kb.Keyboard, []telegram.KeyboardButton{{Text: newReportRequest}})
	return kb
}

// requestState maps the persistent keyboard buttons back to the flow.
func (h *Handler) menuRequest(m telegram.Message) (string, bool) {
	switch strings.TrimSpace(m.Text) {
	case textAdminPanelButton:
		return "panel", true
	case newReportRequest:
		return "report", true
	}
	return "", false
}

// SetReportChat tells the flow where to archive report media.
func (h *Handler) SetReportChat(chatID int64) { h.reportChatID = chatID }

// SetPanelURL enables the "پنل مدیریت" Mini App button for admins.
// u is the deployment address, e.g. https://cr0.ir.
func (h *Handler) SetPanelURL(u string) {
	u = strings.TrimSuffix(strings.TrimSpace(u), "/")
	if u == "" {
		h.panelURL = ""
		return
	}
	h.panelURL = u + "/admin/"
}

// panel opens the admin panel as a Telegram Mini App (inside Telegram, no browser).
// Non-admins get no answer at all: the panel must not even look like it exists
// for them, so guessing the command only produces silence.
func (h *Handler) panel(ctx context.Context, m telegram.Message) error {
	if !h.isPanelUser(m.From.ID) {
		return nil
	}
	if !strings.HasPrefix(h.panelURL, "https://") {
		// Telegram only opens Mini Apps served over HTTPS.
		return h.sendHint(ctx, m.Chat.ID, fmt.Sprintf(textPanelNeedsHTTPS, h.panelURL), nil)
	}
	// The keyboard button opens the Mini App without any extra message; keep a
	// tappable copy pinned as well for chats where the keyboard is hidden.
	kb := &telegram.InlineKeyboardMarkup{
		InlineKeyboard: [][]telegram.InlineKeyboardButton{telegram.WebAppButton(textAdminPanelButton, h.panelURL)},
	}
	_, err := h.send(ctx, m.Chat.ID, textPanelOpened, kb)
	return err
}
