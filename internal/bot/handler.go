package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"bugbot/internal/domain"
	"bugbot/internal/store"
	"bugbot/internal/telegram"
)

type Handler struct {
	api       telegram.API
	db        *store.Store
	isAdminFn func(userID int64) bool
	// adminIDs are told about every new report (empty disables the notice).
	adminIDs []int64
	// panelURL is the Mini App address shown to admins (empty disables the button).
	panelURL string
	// reportChatID is where stored media is re-sent so this bot owns fresh file IDs.
	reportChatID int64
}

// New builds the report handler. isAdmin is optional; when nil, admin
// conveniences in the bot are simply off.
func New(api telegram.API, db *store.Store, isAdmin ...func(int64) bool) *Handler {
	h := &Handler{api: api, db: db}
	if len(isAdmin) > 0 {
		h.isAdminFn = isAdmin[0]
	}
	return h
}

// SetAdmins lists the users that get a notice for every submitted report.
func (h *Handler) SetAdmins(ids []int64) { h.adminIDs = ids }

const (
	cbCancel    = "cancel"
	cbResume    = "resume"
	cbRestart   = "restart"
	cbMediaYes  = "media:yes"
	cbMediaNo   = "media:no"
	cbSkipMedia = "media:skip"
	cbCatPrefix = "cat:"
	cbCatYes    = "cat:yes"
	cbCatNo     = "cat:no"
	cbSkipDesc  = "desc:skip"
	cbSubmitYes = "submit:yes"
	cbSubmitNo  = "submit:no"
)

func kbCancel() *telegram.InlineKeyboardMarkup {
	return telegram.Rows(telegram.Button("لغو گزارش", cbCancel))
}

// kbCancelOrSkipMedia is the first step: media is optional, so the user can go
// straight to the category.
func kbCancelOrSkipMedia() *telegram.InlineKeyboardMarkup {
	return telegram.Rows(
		telegram.Button("بدون تصویر ادامه بده", cbSkipMedia),
		telegram.Button("لغو گزارش", cbCancel),
	)
}

func kbStartMenu() *telegram.InlineKeyboardMarkup {
	return telegram.Rows(
		telegram.Button("ادامه گزارش", cbResume),
		telegram.Button("لغو و شروع مجدد", cbRestart),
	)
}

func kbNewReport(post string) *telegram.InlineKeyboardMarkup {
	return telegram.Rows(telegram.Button("ثبت گزارش جدید", post))
}

func kbConfirmMedia() *telegram.InlineKeyboardMarkup {
	return telegram.Rows(
		telegram.Button("بله، تأیید می‌کنم", cbMediaYes),
		telegram.Button("خیر، ارسال مجدد", cbMediaNo),
		telegram.Button("لغو گزارش", cbCancel),
	)
}

func kbCategories() *telegram.InlineKeyboardMarkup {
	rows := [][]telegram.InlineKeyboardButton{}
	var row []telegram.InlineKeyboardButton
	for _, c := range domain.Categories {
		row = append(row, telegram.InlineKeyboardButton{
			Text:         c,
			CallbackData: cbCatPrefix + domain.CategoryKey(c),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, telegram.Button("لغو گزارش", cbCancel))
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func kbConfirmCategory() *telegram.InlineKeyboardMarkup {
	return telegram.Rows(
		telegram.Button("بله، تأیید می‌کنم", cbCatYes),
		telegram.Button("خیر، انتخاب مجدد", cbCatNo),
		telegram.Button("لغو گزارش", cbCancel),
	)
}

func kbDescription() *telegram.InlineKeyboardMarkup {
	return telegram.Rows(
		telegram.Button("رد شدن از این مرحله", cbSkipDesc),
		telegram.Button("لغو گزارش", cbCancel),
	)
}

func kbConfirmSubmit() *telegram.InlineKeyboardMarkup {
	return telegram.Rows(
		telegram.Button("بله، ثبت گزارش", cbSubmitYes),
		telegram.Button("خیر، لغو گزارش", cbSubmitNo),
	)
}

func (h *Handler) HandleUpdate(ctx context.Context, u telegram.Update) {
	switch {
	case u.Message != nil:
		if err := h.handleMessage(ctx, *u.Message); err != nil {
			log.Printf("message update %d: %v", u.UpdateID, err)
		}
	case u.CallbackQuery != nil:
		if err := h.handleCallback(ctx, *u.CallbackQuery); err != nil {
			log.Printf("callback update %d: %v", u.UpdateID, err)
		}
	}
}

func promptFor(d domain.Draft) (string, *telegram.InlineKeyboardMarkup) {
	switch d.State {
	case domain.StateAwaitingMedia:
		return textAskMedia, kbCancelOrSkipMedia()
	case domain.StateConfirmingMedia:
		return textConfirmMedia, kbConfirmMedia()
	case domain.StateSelectingCategory:
		return textAskCategory, kbCategories()
	case domain.StateConfirmingCategory:
		return fmt.Sprintf(textConfirmCategory, d.Category), kbConfirmCategory()
	case domain.StateAwaitingDescription:
		if d.Desc != "" {
			// Text sent at the media step already serves as the description; show it
			// so the user knows it is kept and can replace it.
			return fmt.Sprintf(textAskDescriptionUpdate, d.Desc), kbDescription()
		}
		return textAskDescription, kbDescription()
	case domain.StateConfirmingSubmit:
		return summary(d), kbConfirmSubmit()
	}
	return textUnknown, kbCancel()
}

func summary(d domain.Draft) string {
	desc := strings.TrimSpace(d.Desc)
	if desc == "" {
		desc = textNoDescription
	}
	return fmt.Sprintf(textSummary, mediaLabel(d.Media), d.Category, desc)
}

func mediaLabel(media []domain.Media) string {
	switch {
	case len(media) == 0:
		return textNoMedia
	case len(media) > 1:
		return fmt.Sprintf("%d فایل", len(media))
	case media[0].Type == domain.MediaVideo:
		return "ویدئوی ارسال‌شده"
	}
	return "تصویر ارسال‌شده"
}

func userInfo(u telegram.User) domain.UserInfo {
	return domain.UserInfo{ID: u.ID, Username: u.Username, FirstName: u.FirstName, LastName: u.LastName}
}

func (h *Handler) loadDraft(ctx context.Context, userID int64) (domain.Draft, bool, error) {
	d, err := h.db.DraftFor(ctx, userID)
	if errors.Is(err, store.ErrNoDraftSentinel) {
		return domain.Draft{}, false, nil
	}
	return d, err == nil, err
}

func flowCommand(text string) (string, bool) {
	cmd, _, _ := strings.Cut(strings.TrimSpace(text), " ")
	if at := strings.IndexByte(cmd, '@'); at > 0 {
		cmd = cmd[:at]
	}
	switch cmd {
	case "/start", "/report", "/cancel", "/panel":
		return cmd, true
	}
	return "", false
}

// replacePrompt makes text/kb the current prompt after something the user sent.
// A fresh bubble is posted (right below the user's message) and the previous
// prompt is deleted, so the question is always the latest message in the chat
// and never sits above a photo the user just sent.
func (h *Handler) replacePrompt(ctx context.Context, d *domain.Draft, userMsgID int64, text string, kb *telegram.InlineKeyboardMarkup) error {
	chatID := d.User.ID
	old := d.PromptMessageID
	id, err := h.send(ctx, chatID, text, kb)
	if err != nil {
		return err
	}
	if old != 0 && old != userMsgID && old != id {
		_ = h.api.DeleteMessage(ctx, chatID, old)
	}
	d.PromptMessageID = id
	return h.db.SaveDraft(ctx, *d)
}

// editPrompt rewrites the current prompt in place. Button presses happen on the
// prompt bubble itself, so re-sending it on every tap would only flicker.
func (h *Handler) editPrompt(ctx context.Context, d *domain.Draft, text string, kb *telegram.InlineKeyboardMarkup) error {
	id := d.PromptMessageID
	if id == 0 {
		nid, err := h.send(ctx, d.User.ID, text, kb)
		if err != nil {
			return err
		}
		d.PromptMessageID = nid
		return h.db.SaveDraft(ctx, *d)
	}
	err := h.api.EditMessageText(ctx, d.User.ID, id, text, kb)
	if err == nil || isNotModified(err) {
		return h.db.SaveDraft(ctx, *d)
	}
	if !isNotEditable(err) {
		return err
	}
	// The prompt is gone (cleared history or too old): post it anew.
	nid, sendErr := h.send(ctx, d.User.ID, text, kb)
	if sendErr != nil {
		return sendErr
	}
	d.PromptMessageID = nid
	return h.db.SaveDraft(ctx, *d)
}

// send posts a message and returns its Telegram message ID (0 if the API gave none).
func (h *Handler) send(ctx context.Context, chatID int64, text string, kb *telegram.InlineKeyboardMarkup) (int64, error) {
	return h.api.SendMessage(ctx, chatID, text, kb)
}

// isNotModified reports whether the API rejected the edit because the message
// already showed exactly this content and keyboard.
func isNotModified(err error) bool {
	var apiErr *telegram.Error
	return err != nil && errors.As(err, &apiErr) && apiErr.IsNotModified()
}

func isNotEditable(err error) bool {
	var apiErr *telegram.Error
	return err != nil && errors.As(err, &apiErr) && apiErr.IsNotEditable()
}

func (h *Handler) handleMessage(ctx context.Context, m telegram.Message) error {
	if m.From == nil || m.From.IsBot {
		return nil
	}
	media := extractMedia(m)
	text := strings.TrimSpace(m.Text)
	if cmd, ok := flowCommand(text); ok {
		switch cmd {
		case "/start", "/report":
			return h.start(ctx, m)
		case "/cancel":
			return h.cancelAny(ctx, m)
		case "/panel":
			return h.panel(ctx, m)
		}
	}

	if req, ok := h.menuRequest(m); ok {
		switch req {
		case "panel":
			return h.panel(ctx, m)
		case "report":
			if d, has, err := h.loadDraft(ctx, m.From.ID); err != nil {
				return err
			} else if has && d.State != domain.StateChatting {
				return h.resume(ctx, &d)
			}
			return h.start(ctx, m)
		}
	}

	d, has, err := h.loadDraft(ctx, m.From.ID)
	if err != nil {
		return err
	}
	if !has {
		if len(media) > 0 {
			// Media sent without starting a report: begin a draft with that media
			// and ask for confirmation right below it.
			nd := domain.Draft{User: userInfo(*m.From), Media: media, State: domain.StateConfirmingMedia}
			text, kb := promptFor(nd)
			return h.replacePrompt(ctx, &nd, int64(m.MessageID), text, kb)
		}
		return h.sendHint(ctx, m.Chat.ID, textNoDraft, nil)
	}

	if d.State == domain.StateChatting {
		return h.forwardToAdmin(ctx, m, d)
	}

	var reply string
	var kb *telegram.InlineKeyboardMarkup
	switch d.State {
	case domain.StateAwaitingMedia:
		if len(media) == 0 {
			// Media is optional: a message without media moves the report on to the
			// category. Text sent here is the natural place for the description, so
			// keep it instead of throwing it away.
			if text != "" {
				d.Desc = text
			}
			d.State = domain.StateSelectingCategory
			reply, kb = promptFor(d)
			break
		}
		d.Media = media
		d.MediaMessageID = int64(m.MessageID)
		if fresh, ok := h.secureMedia(ctx, media); ok {
			d.Media = fresh
		}
		d.State = domain.StateConfirmingMedia
		reply, kb = promptFor(d)
	case domain.StateAwaitingDescription:
		d.Desc = text
		d.State = domain.StateConfirmingSubmit
		reply, kb = promptFor(d)
	default:
		reply, kb = promptFor(d)
	}
	return h.replacePrompt(ctx, &d, int64(m.MessageID), reply, kb)
}

// secureMedia re-sends the user's media to the report archive chat and keeps the
// new file IDs. Telegram file IDs are scoped to the bot that received the file;
// re-sending here guarantees the panel can always load it later, whatever bot
// token was configured when a report was created.
func (h *Handler) secureMedia(ctx context.Context, media []domain.Media) ([]domain.Media, bool) {
	if h.reportChatID == 0 || len(media) == 0 {
		return nil, false
	}
	out := make([]domain.Media, 0, len(media))
	for _, m := range media {
		f, err := h.api.GetFile(ctx, m.FileID)
		if err != nil || f.FilePath == "" {
			log.Printf("archive media %s: %v", m.FileID, err)
			continue
		}
		raw, err := h.api.DownloadFile(ctx, f.FilePath)
		if err != nil {
			log.Printf("archive media download %s: %v", f.FilePath, err)
			continue
		}
		msg, err := h.api.SendMedia(ctx, h.reportChatID, string(m.Type), mediaFilename(m, f.FilePath), raw, "")
		if err != nil {
			log.Printf("archive media upload: %v", err)
			continue
		}
		if typ, id, uid, ok := h.api.FileFromMessage(msg); ok {
			out = append(out, domain.Media{Type: domain.MediaType(typ), FileID: id, FileUniqueID: uid})
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func mediaFilename(m domain.Media, path string) string {
	if m.Type == domain.MediaVideo {
		if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")); ext == "mov" || ext == "webm" {
			return "report." + ext
		}
		return "report.mp4"
	}
	if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")); ext == "png" || ext == "webp" {
		return "report." + ext
	}
	return "report.jpg"
}

// cancelAny ends a chat with the team, or cancels the pending report.
func (h *Handler) cancelAny(ctx context.Context, m telegram.Message) error {
	d, has, err := h.loadDraft(ctx, m.From.ID)
	if err != nil {
		return err
	}
	if has && d.State == domain.StateChatting {
		// CloseChat removes the chatting row; nothing is left for later messages
		// to revive, so the next message simply starts a new report.
		if _, err := h.db.CloseChat(ctx, m.From.ID); err != nil {
			return err
		}
		return h.sendHint(ctx, m.Chat.ID, textChatEnded, nil)
	}
	return h.cancel(ctx, m.Chat.ID, m.From.ID)
}

// forwardToAdmin sends the user's text into the report conversation with the team.
func (h *Handler) forwardToAdmin(ctx context.Context, m telegram.Message, d domain.Draft) error {
	text := strings.TrimSpace(m.Text)
	if text == "" || d.ReportID == "" {
		return h.sendHint(ctx, m.Chat.ID, textChatWaiting, kbCancelChat())
	}
	if _, err := h.db.AppendChat(ctx, domain.ChatMessage{
		ReportID: d.ReportID,
		Sender:   domain.SenderUser,
		Text:     text,
	}); err != nil {
		return err
	}
	// Remember where to reach this user (privacy mode may hide the user id).
	if err := h.db.LinkChat(ctx, m.From.ID, m.Chat.ID, d.ReportID); err != nil {
		return err
	}
	_, err := h.send(ctx, m.Chat.ID, textUserSays, nil)
	return err
}

// sendHint answers without touching the stored prompt.
func (h *Handler) sendHint(ctx context.Context, chatID int64, text string, kb *telegram.InlineKeyboardMarkup) error {
	_, err := h.send(ctx, chatID, text, kb)
	return err
}

func extractMedia(m telegram.Message) []domain.Media {
	switch {
	case len(m.Photo) > 0:
		p := m.Photo[len(m.Photo)-1]
		return []domain.Media{{Type: domain.MediaImage, FileID: p.FileID, FileUniqueID: p.FileUniqueID}}
	case m.Video != nil:
		return []domain.Media{{Type: domain.MediaVideo, FileID: m.Video.FileID, FileUniqueID: m.Video.FileUniqueID}}
	}
	return nil
}

func (h *Handler) start(ctx context.Context, m telegram.Message) error {
	d, has, err := h.loadDraft(ctx, m.From.ID)
	if err != nil {
		return err
	}
	if has {
		return h.replacePrompt(ctx, &d, int64(m.MessageID), textDraftExists, kbStartMenu())
	}

	if h.isPanelUser(m.From.ID) && m.Chat.Type == "private" {
		// Admins get the report flow as the persistent keyboard with the panel
		// button on top; the panel is one tap away from now on.
		_, err := h.api.SendMessageWithReplyKeyboard(ctx, m.Chat.ID, textWelcome+"\n\n"+textAdminHint, adminMenu(h.panelURL))
		return err
	}

	d = domain.Draft{User: userInfo(*m.From), State: domain.StateAwaitingMedia}
	return h.replacePrompt(ctx, &d, int64(m.MessageID), textWelcome, kbCancelOrSkipMedia())
}

func (h *Handler) resume(ctx context.Context, d *domain.Draft) error {
	text, kb := promptFor(*d)
	return h.editPrompt(ctx, d, text, kb)
}

func (h *Handler) cancel(ctx context.Context, chatID, userID int64) error {
	d, has, err := h.loadDraft(ctx, userID)
	if err != nil {
		return err
	}
	if _, err := h.db.DeleteDraft(ctx, userID); err != nil {
		return err
	}
	if !has {
		return h.sendHint(ctx, chatID, textNoDraft, nil)
	}
	if d.PromptMessageID == 0 {
		return h.sendHint(ctx, chatID, textCancelled, kbNewReport(cbRestart))
	}
	return h.api.EditMessageText(ctx, chatID, d.PromptMessageID, textCancelled, kbNewReport(cbRestart))
}

func (h *Handler) handleCallback(ctx context.Context, cb telegram.CallbackQuery) error {
	chatID := cb.From.ID
	var promptID int64
	if cb.Message != nil {
		chatID = cb.Message.Chat.ID
		promptID = cb.Message.MessageID
	}
	defer func() {
		if err := h.api.AnswerCallback(ctx, cb.ID, ""); err != nil {
			log.Printf("answer callback: %v", err)
		}
	}()

	if cb.Data == cbRestart {
		if _, err := h.db.DeleteDraft(ctx, cb.From.ID); err != nil {
			return err
		}
		d := domain.Draft{User: userInfo(cb.From), State: domain.StateAwaitingMedia, PromptMessageID: promptID}
		return h.editPrompt(ctx, &d, textWelcome, kbCancelOrSkipMedia())
	}

	d, has, err := h.loadDraft(ctx, cb.From.ID)
	if err != nil {
		return err
	}

	if cb.Data == cbCancel || cb.Data == cbSubmitNo {
		if _, err := h.db.DeleteDraft(ctx, cb.From.ID); err != nil {
			return err
		}
		if !has {
			return h.editOrSend(ctx, chatID, promptID, textNoDraft, kbStartMenu())
		}
		return h.editOrSend(ctx, chatID, promptID, textCancelled, kbNewReport(cbRestart))
	}

	if !has {
		return h.editOrSend(ctx, chatID, promptID, textNoDraft, kbStartMenu())
	}

	// A button belongs to the bubble that holds it; keep the draft pointing there.
	if promptID != 0 {
		d.PromptMessageID = promptID
	}

	if cb.Data == cbResume {
		return h.resume(ctx, &d)
	}

	if allowed := allowedCallbacks(d.State); !allowed[cb.Data] {
		text, kb := promptFor(d)
		return h.editOrSend(ctx, chatID, promptID, textStale+"\n\n"+text, kb)
	}

	switch {
	case cb.Data == cbMediaYes:
		d.State = domain.StateSelectingCategory
	case cb.Data == cbMediaNo:
		d.Media = nil
		d.State = domain.StateAwaitingMedia
	case cb.Data == cbSkipMedia:
		d.Media = nil
		d.MediaMessageID = 0
		d.State = domain.StateSelectingCategory
	case cb.Data != cbCatYes && cb.Data != cbCatNo && strings.HasPrefix(cb.Data, cbCatPrefix):
		cat, ok := domain.CategoryByKey(strings.TrimPrefix(cb.Data, cbCatPrefix))
		if !ok {
			text, kb := promptFor(d)
			return h.editOrSend(ctx, chatID, promptID, textUnknown+"\n\n"+text, kb)
		}
		d.Category = cat
		d.State = domain.StateConfirmingCategory
	case cb.Data == cbCatNo:
		d.Category = ""
		d.State = domain.StateSelectingCategory
	case cb.Data == cbCatYes:
		d.State = domain.StateAwaitingDescription
	case cb.Data == cbSkipDesc:
		// Keep any description already collected (a text sent at the media step);
		// skipping only closes this step.
		d.State = domain.StateConfirmingSubmit
	case cb.Data == cbSubmitYes:
		return h.submit(ctx, chatID, promptID, d)
	default:
		text, kb := promptFor(d)
		return h.editOrSend(ctx, chatID, promptID, textUnknown+"\n\n"+text, kb)
	}

	text, kb := promptFor(d)
	return h.editPrompt(ctx, &d, text, kb)
}

// editOrSend edits the tapped message in place, or posts a new one if unknown.
func (h *Handler) editOrSend(ctx context.Context, chatID, messageID int64, text string, kb *telegram.InlineKeyboardMarkup) error {
	if messageID == 0 {
		return h.sendHint(ctx, chatID, text, kb)
	}
	return h.api.EditMessageText(ctx, chatID, messageID, text, kb)
}

func allowedCallbacks(s domain.State) map[string]bool {
	switch s {
	case domain.StateAwaitingMedia:
		return map[string]bool{cbSkipMedia: true}
	case domain.StateConfirmingMedia:
		return map[string]bool{cbMediaYes: true, cbMediaNo: true}
	case domain.StateSelectingCategory:
		m := map[string]bool{}
		for _, c := range domain.Categories {
			m[cbCatPrefix+domain.CategoryKey(c)] = true
		}
		return m
	case domain.StateConfirmingCategory:
		return map[string]bool{cbCatYes: true, cbCatNo: true}
	case domain.StateAwaitingDescription:
		return map[string]bool{cbSkipDesc: true}
	case domain.StateConfirmingSubmit:
		return map[string]bool{cbSubmitYes: true}
	}
	return map[string]bool{}
}

func (h *Handler) submit(ctx context.Context, chatID, promptID int64, d domain.Draft) error {
	id, err := store.NewReportID()
	if err != nil {
		return err
	}
	report := domain.Report{
		ID:          id,
		User:        d.User,
		Media:       d.Media,
		Category:    d.Category,
		Description: strings.TrimSpace(d.Desc),
	}
	err = h.db.InsertReport(ctx, report)
	if errors.Is(err, store.ErrNoDraftSentinel) {
		return h.editOrSend(ctx, chatID, promptID, textNoDraft, kbNewReport(cbRestart))
	}
	if err != nil {
		return err
	}
	if err := h.editOrSend(ctx, chatID, promptID, textSuccess, kbNewReport(cbRestart)); err != nil {
		return err
	}
	h.notifyAdmins(ctx, report)
	if h.isPanelUser(d.User.ID) && h.panelURL != "" {
		_, err := h.api.SendMessageWithReplyKeyboard(ctx, chatID, textAdminHint, adminMenu(h.panelURL))
		return err
	}
	return nil
}
