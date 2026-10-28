package bot_test

import (
	"context"
	"strings"
	"testing"

	"bugbot/internal/bot"
	"bugbot/internal/domain"
	"bugbot/internal/telegram"
)

func TestStartShowsWelcomeAndCancelButton(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	m := e.last()
	if !strings.Contains(m.Text, "به ربات گزارش باگ کت خوش آمدی") {
		t.Fatalf("unexpected welcome: %q", m.Text)
	}
	if m.KB == nil || m.KB.InlineKeyboard[0][0].CallbackData != "cancel" {
		t.Fatalf("cancel button missing")
	}
}

func TestReportCommandStartsFlow(t *testing.T) {
	e := newEnv(t)
	e.text("/report")
	m := e.last()
	if !strings.Contains(m.Text, "تصویر یا ویدئوی مربوط به باگ را ارسال کن") {
		t.Fatalf("unexpected: %q", m.Text)
	}
	if m.KB == nil || m.KB.InlineKeyboard[0][0].CallbackData != "cancel" {
		t.Fatal("cancel button missing")
	}
	e.photo("p1")
	if !strings.HasPrefix(e.last().Text, "آیا از ارسال این تصویر یا ویدئو برای گزارش باگ مطمئن هستی؟") {
		t.Fatalf("unexpected: %q", e.last().Text)
	}
}

func TestReportCommandWithUsernameSuffix(t *testing.T) {
	e := newEnv(t)
	e.text("/report@bugbot")
	if !strings.Contains(e.last().Text, "به ربات گزارش باگ کت خوش آمدی") {
		t.Fatalf("unexpected: %q", e.last().Text)
	}
}

func TestReportCommandWithExistingDraftAsks(t *testing.T) {
	e := newEnv(t)
	e.text("/report")
	e.photo("p1")
	e.text("/report")
	if !strings.Contains(e.last().Text, "گزارش نیمه‌تمام") {
		t.Fatalf("unexpected: %q", e.last().Text)
	}
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateConfirmingMedia {
		t.Fatalf("state = %s", d.State)
	}
}

func TestPhotoAccepted(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("photo-big")
	m := e.last()
	if !strings.HasPrefix(m.Text, "آیا از ارسال این تصویر یا ویدئو برای گزارش باگ مطمئن هستی؟") {
		t.Fatalf("unexpected prompt: %q", m.Text)
	}
	d, err := e.db.DraftFor(context.Background(), testUserID)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != domain.StateConfirmingMedia {
		t.Fatalf("state = %s", d.State)
	}
	if len(d.Media) != 1 || d.Media[0].FileID != "photo-big" || d.Media[0].Type != domain.MediaImage {
		t.Fatalf("media = %+v", d.Media)
	}
}

func TestVideoAccepted(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.video("video-1")
	d, err := e.db.DraftFor(context.Background(), testUserID)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != domain.StateConfirmingMedia || d.Media[0].Type != domain.MediaVideo || d.Media[0].FileUniqueID != "u-video-1" {
		t.Fatalf("draft = %+v", d)
	}
}

func TestInvalidMediaRejected(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.text("سلام، یک توضیح")
	if !strings.Contains(e.last().Text, "مدیای معتبر") {
		t.Fatalf("expected rejection: %q", e.last().Text)
	}
	d, err := e.db.DraftFor(context.Background(), testUserID)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != domain.StateAwaitingMedia || len(d.Media) != 0 {
		t.Fatalf("draft changed: %+v", d)
	}
}

func TestMediaRejectionClearsMedia(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	e.callback("media:no")
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateAwaitingMedia || len(d.Media) != 0 {
		t.Fatalf("draft = %+v", d)
	}
	e.photo("p2")
	d, _ = e.db.DraftFor(context.Background(), testUserID)
	if len(d.Media) != 1 || d.Media[0].FileID != "p2" {
		t.Fatalf("media = %+v", d.Media)
	}
}

func TestCategorySelectionAndConfirmation(t *testing.T) {
	e := newEnv(t)
	e.goToCategory()
	m := e.last()
	if m.Text != "این مشکل در کدام بخش کت اتفاق افتاده است؟" {
		t.Fatalf("unexpected: %q", m.Text)
	}
	seen := map[string]bool{}
	for _, row := range m.KB.InlineKeyboard {
		for _, b := range row {
			seen[b.CallbackData] = true
		}
	}
	for i := range domain.Categories {
		if !seen["cat:"+domain.CategoryKey(domain.Categories[i])] {
			t.Fatalf("missing category %s", domain.Categories[i])
		}
	}

	e.callback("cat:3") // تماس صوتی
	m = e.last()
	if m.Text != "آیا مطمئنی که این مشکل مربوط به بخش «تماس صوتی» است؟" {
		t.Fatalf("unexpected: %q", m.Text)
	}
	e.callback("cat:yes")
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateAwaitingDescription || d.Category != "تماس صوتی" {
		t.Fatalf("draft = %+v", d)
	}
}

func TestCategoryRejectionReturnsToSelection(t *testing.T) {
	e := newEnv(t)
	e.goToCategory()
	e.callback("cat:2")
	e.callback("cat:no")
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateSelectingCategory || d.Category != "" {
		t.Fatalf("draft = %+v", d)
	}
	if e.last().Text != "این مشکل در کدام بخش کت اتفاق افتاده است؟" {
		t.Fatalf("unexpected: %q", e.last().Text)
	}
}

func TestDescriptionProvided(t *testing.T) {
	e := newEnv(t)
	e.goToDescription()
	e.text("وقتی پیام می‌فرستم، ارسال نمی‌شود")
	m := e.last()
	if !strings.Contains(m.Text, "وقتی پیام می‌فرستم") || !strings.Contains(m.Text, "دسته‌بندی: گروه‌ها") {
		t.Fatalf("summary = %q", m.Text)
	}
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateConfirmingSubmit || d.Desc != "وقتی پیام می‌فرستم، ارسال نمی‌شود" {
		t.Fatalf("draft = %+v", d)
	}
}

func TestDescriptionSkipped(t *testing.T) {
	e := newEnv(t)
	e.goToDescription()
	e.callback("desc:skip")
	m := e.last()
	if !strings.Contains(m.Text, "بدون توضیحات") {
		t.Fatalf("summary = %q", m.Text)
	}
}

func TestEmptyTextAsDescriptionDoesNotBreak(t *testing.T) {
	e := newEnv(t)
	e.goToDescription()
	e.text("   ")
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateConfirmingSubmit {
		t.Fatalf("state = %s", d.State)
	}
	if !strings.Contains(e.last().Text, "بدون توضیحات") {
		t.Fatalf("summary = %q", e.last().Text)
	}
}

func TestFinalConfirmSavesReport(t *testing.T) {
	e := newEnv(t)
	e.goToDescription()
	e.text("توضیح")
	e.callback("submit:yes")
	m := e.last()
	if !strings.HasPrefix(m.Text, "گزارش باگ تو با موفقیت ثبت شد.") {
		t.Fatalf("unexpected: %q", m.Text)
	}
	if n := e.reportCount(); n != 1 {
		t.Fatalf("reports = %d", n)
	}
	if _, err := e.db.DraftFor(context.Background(), testUserID); err == nil {
		t.Fatal("draft should be gone")
	}
}

func TestSubmittedReportPersistsMediaIDs(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.video("v9")
	e.callback("media:yes")
	e.callback("cat:0")
	e.callback("cat:yes")
	e.callback("desc:skip")
	e.callback("submit:yes")

	mediaJSON, cat := e.reportRow(t)
	if !strings.Contains(mediaJSON, "v9") || !strings.Contains(mediaJSON, "u-v9") {
		t.Fatalf("media_json = %s", mediaJSON)
	}
	if cat != "چت‌های خصوصی" {
		t.Fatalf("category = %s", cat)
	}
}

func TestDuplicateSubmitDoesNotCreateSecondReport(t *testing.T) {
	e := newEnv(t)
	e.goToDescription()
	e.callback("desc:skip")
	e.callback("submit:yes")
	e.callback("submit:yes")
	e.callback("submit:yes")
	if n := e.reportCount(); n != 1 {
		t.Fatalf("reports = %d", n)
	}
	if !strings.Contains(e.last().Text, "گزارش فعالی وجود ندارد") {
		t.Fatalf("unexpected: %q", e.last().Text)
	}
}

func TestCancelAtEveryStage(t *testing.T) {
	steps := []func(e *env){
		func(e *env) {},
		func(e *env) { e.photo("p") },
		func(e *env) { e.photo("p"); e.callback("media:yes") },
		func(e *env) { e.photo("p"); e.callback("media:yes"); e.callback("cat:1") },
		func(e *env) { e.goToDescription() },
		func(e *env) { e.goToDescription(); e.callback("desc:skip") },
	}
	for i, setup := range steps {
		e := newEnv(t)
		e.text("/start")
		setup(e)
		e.callback("cancel")
		if _, err := e.db.DraftFor(context.Background(), testUserID); err == nil {
			t.Fatalf("step %d: draft should be cancelled", i)
		}
		if n := e.reportCount(); n != 0 {
			t.Fatalf("step %d: reports = %d", i, n)
		}
		if e.last().Text != "گزارش لغو شد." {
			t.Fatalf("step %d: unexpected: %q", i, e.last().Text)
		}
	}
}

func TestCancelCommandWorks(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p")
	e.text("/cancel")
	if _, err := e.db.DraftFor(context.Background(), testUserID); err == nil {
		t.Fatal("draft should be cancelled by /cancel")
	}
}

func TestFinalCancelDoesNotSave(t *testing.T) {
	e := newEnv(t)
	e.goToDescription()
	e.callback("desc:skip")
	e.callback("submit:no")
	if n := e.reportCount(); n != 0 {
		t.Fatalf("reports = %d", n)
	}
	if _, err := e.db.DraftFor(context.Background(), testUserID); err == nil {
		t.Fatal("draft should be gone")
	}
}

func TestResumeAfterRestart(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	e.reopen()
	e.text("/start")
	m := e.last()
	if !strings.Contains(m.Text, "گزارش نیمه‌تمام") {
		t.Fatalf("unexpected: %q", m.Text)
	}
	e.callback("resume")
	if !strings.HasPrefix(e.last().Text, "آیا از ارسال این تصویر یا ویدئو برای گزارش باگ مطمئن هستی؟") {
		t.Fatalf("resume prompt: %q", e.last().Text)
	}
	e.callback("media:yes")
	e.callback("cat:5")
	e.callback("cat:yes")
	e.reopen()
	e.text("/start")
	e.callback("resume")
	if !strings.Contains(e.last().Text, "توضیح بیشتری") {
		t.Fatalf("resume prompt: %q", e.last().Text)
	}
	e.text("توضیح نهایی")
	e.reopen()
	e.text("/start")
	e.callback("resume")
	if !strings.Contains(e.last().Text, "توضیح نهایی") {
		t.Fatalf("resume summary: %q", e.last().Text)
	}
	e.callback("submit:yes")
	if n := e.reportCount(); n != 1 {
		t.Fatalf("reports = %d", n)
	}
}

func TestRestartDiscardsDraft(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	e.text("/start")
	e.callback("restart")
	d, err := e.db.DraftFor(context.Background(), testUserID)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != domain.StateAwaitingMedia || len(d.Media) != 0 {
		t.Fatalf("draft = %+v", d)
	}
}

func TestStaleCallbackIsIgnored(t *testing.T) {
	e := newEnv(t)
	e.goToDescription()
	// old "media:yes" from a previous step must not skip ahead
	e.callback("media:yes")
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateAwaitingDescription {
		t.Fatalf("state = %s", d.State)
	}
	if !strings.Contains(e.last().Text, "این دکمه دیگر معتبر نیست") {
		t.Fatalf("unexpected: %q", e.last().Text)
	}
}

func TestUnknownCallbackDataIsHandled(t *testing.T) {
	e := newEnv(t)
	e.goToCategory()
	e.callback("cat:zzz")
	if e.last().KB == nil {
		t.Fatal("expected guidance with keyboard")
	}
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateSelectingCategory {
		t.Fatalf("state = %s", d.State)
	}
}

func TestCallbackWithoutDraft(t *testing.T) {
	e := newEnv(t)
	e.callback("media:yes")
	if e.last().Text != "گزارش فعالی وجود ندارد.\nبرای شروع گزارش جدید، /start را بفرست." {
		t.Fatalf("unexpected: %q", e.last().Text)
	}
	if len(e.api.answered) != 1 {
		t.Fatalf("callback not answered")
	}
}

func TestUnrelatedTextGivesGuidance(t *testing.T) {
	e := newEnv(t)
	e.goToCategory()
	e.text("چی؟")
	if e.last().KB == nil || e.last().Text != "این مشکل در کدام بخش کت اتفاق افتاده است؟" {
		t.Fatalf("unexpected: %q", e.last().Text)
	}
}

func TestCallbackAlwaysAnswered(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p")
	e.callback("media:yes")
	if len(e.api.answered) != 1 || e.api.answered[0] != "cb-media:yes" {
		t.Fatalf("answered = %v", e.api.answered)
	}
}

func TestKeyboardButtonsUseCallbackData(t *testing.T) {
	e := newEnv(t)
	e.goToCategory()
	for _, row := range e.last().KB.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == "" || b.Text == "" {
				t.Fatalf("empty button: %+v", b)
			}
		}
	}
}

func TestFlowKeepsOneCurrentPrompt(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	e.callback("media:yes")
	e.callback("cat:7")
	e.callback("cat:yes")
	e.text("توضیحات")
	t.Logf("deleted before submit: %v", e.api.deleted)
	e.callback("submit:yes")

	// Every user message replaced the prompt: /start, the photo, the description.
	if got := len(e.api.sent); got != 3 {
		t.Fatalf("prompts posted = %d, want 3 (start, photo, description): %q", got, e.api.sentTexts())
	}
	if got := len(e.api.deleted); got != 2 {
		t.Fatalf("stale prompts deleted = %d, want 2", got)
	}
	for _, id := range e.api.deleted {
		if id == 1_000_001 {
			t.Fatal("the photo must never be deleted")
		}
	}
	if !strings.HasPrefix(e.api.lastEdit(t).Text, "گزارش باگ تو با موفقیت ثبت شد.") {
		t.Fatalf("final edit: %q", e.api.lastEdit(t).Text)
	}
	if n := e.reportCount(); n != 1 {
		t.Fatalf("reports = %d", n)
	}
}

func TestPromptAfterPhotoSitsBelowIt(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	promptBefore := e.api.promptID()
	e.photo("p1")
	m := e.last()
	if m.msgID >= userIDOffset || m.msgID == promptBefore {
		t.Fatalf("confirmation must be a fresh bot message below the photo, got %d", m.msgID)
	}
	if !strings.HasPrefix(m.Text, "آیا از ارسال این تصویر") {
		t.Fatalf("unexpected prompt %q", m.Text)
	}
	if len(e.api.deleted) != 1 {
		t.Fatalf("the welcome message should be deleted, deleted = %v", e.api.deleted)
	}
	if d, _ := e.db.DraftFor(context.Background(), testUserID); d.PromptMessageID != m.msgID {
		t.Fatalf("stored prompt %d, want %d", d.PromptMessageID, m.msgID)
	}
}

func TestPromptSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	storedBefore, _ := e.db.DraftFor(context.Background(), testUserID)
	e.reopen()
	storedAfter, _ := e.db.DraftFor(context.Background(), testUserID)
	if storedAfter.PromptMessageID == 0 || storedAfter.PromptMessageID != storedBefore.PromptMessageID {
		t.Fatalf("prompt id not persisted across restart: %d -> %d", storedBefore.PromptMessageID, storedAfter.PromptMessageID)
	}
	e.api.edited = nil
	e.callback("media:yes")
	if edit := e.api.lastEdit(t); edit.msgID != storedAfter.PromptMessageID {
		t.Fatalf("after restart the flow edited %d instead of the stored prompt %d", edit.msgID, storedAfter.PromptMessageID)
	}
	if d, _ := e.db.DraftFor(context.Background(), testUserID); d.PromptMessageID != storedAfter.PromptMessageID {
		t.Fatalf("prompt id changed after restart: %d", d.PromptMessageID)
	}
}

func TestUneditablePromptFallsBackToNewMessage(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	// The prompt cannot be edited any more (e.g. deleted by the user).
	e.api.editErr = &telegram.Error{Method: "editMessageText", Code: 400, Description: "Bad Request: message to edit not found"}
	e.api.editErrIDs(2)
	e.callback("media:yes")
	m := e.last()
	if !strings.HasPrefix(m.Text, "این مشکل در کدام بخش") {
		t.Fatalf("expected fallback prompt, got %q", m.Text)
	}
	if m.msgID == 0 {
		t.Fatal("fallback prompt id not recorded")
	}
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.PromptMessageID != m.msgID {
		t.Fatalf("stored prompt %d, want %d", d.PromptMessageID, m.msgID)
	}
}

func TestNotModifiedEditIsNotAnError(t *testing.T) {
	e := newEnv(t)
	e.goToCategory()
	e.api.editErr = &telegram.Error{Method: "editMessageText", Code: 400, Description: "Bad Request: message is not modified"}
	e.api.editErrIDs(e.api.promptID())
	e.callback("cat:1")
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.State != domain.StateConfirmingCategory {
		t.Fatalf("state = %s", d.State)
	}
}

func TestSummaryNamesMediaType(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.video("v1")
	e.callback("media:yes")
	e.callback("cat:0")
	e.callback("cat:yes")
	e.callback("desc:skip")
	if !strings.Contains(e.last().Text, "📎 مدیا: ویدئوی ارسال‌شده") {
		t.Fatalf("summary = %q", e.last().Text)
	}
}

func TestPanelCommandOnlyForAdmins(t *testing.T) {
	e := newEnv(t)
	e.h.SetPanelURL("https://cr0.ir")
	e.text("/panel")
	m := e.last()
	if !strings.HasPrefix(m.Text, "✋") && !strings.Contains(m.Text, "فقط برای مدیران") {
		t.Fatalf("non-admin should be refused, got %q", m.Text)
	}
	if m.KB != nil {
		t.Fatal("non-admin must not receive a panel button")
	}

	admin := bot.New(e.api, e.db, func(id int64) bool { return id == testUserID })
	admin.SetPanelURL("https://cr0.ir")
	admin.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 900,
		Message: &telegram.Message{
			MessageID: e.api.userMessage(),
			From:      &telegram.User{ID: testUserID, FirstName: "علی"},
			Chat:      telegram.Chat{ID: testUserID, Type: "private"},
			Text:      "/panel",
		},
	})
	m = e.last()
	if m.KB == nil || len(m.KB.InlineKeyboard[0]) != 1 || m.KB.InlineKeyboard[0][0].WebApp == nil {
		t.Fatalf("admin should receive a Mini App button, got %+v", m.KB)
	}
	if got := m.KB.InlineKeyboard[0][0].WebApp.URL; got != "https://cr0.ir/admin/" {
		t.Fatalf("mini app url = %q", got)
	}
}

func TestPanelWithoutHTTPSExplainsWhy(t *testing.T) {
	e := newEnv(t)
	admin := bot.New(e.api, e.db, func(id int64) bool { return true })
	admin.SetPanelURL("http://localhost:8080")
	admin.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 901,
		Message: &telegram.Message{
			MessageID: e.api.userMessage(),
			From:      &telegram.User{ID: testUserID, FirstName: "علی"},
			Chat:      telegram.Chat{ID: testUserID, Type: "private"},
			Text:      "/panel",
		},
	})
	if !strings.Contains(e.last().Text, "HTTPS") {
		t.Fatalf("expected an HTTPS explanation, got %q", e.last().Text)
	}
}

func TestAdminStartShowsMiniAppKeyboard(t *testing.T) {
	e := newEnv(t)
	admin := bot.New(e.api, e.db, func(id int64) bool { return id == testUserID })
	admin.SetPanelURL("https://cr0.ir")
	send := func(id int64, text string) {
		admin.HandleUpdate(context.Background(), telegram.Update{
			UpdateID: id,
			Message: &telegram.Message{
				MessageID: e.api.userMessage(),
				From:      &telegram.User{ID: testUserID, FirstName: "علی"},
				Chat:      telegram.Chat{ID: testUserID, Type: "private"},
				Text:      text,
			},
		})
	}
	send(910, "/start")
	if len(e.api.replyKeyboards) != 1 {
		t.Fatalf("admin /start should attach a persistent keyboard, got %d", len(e.api.replyKeyboards))
	}
	kb := e.api.replyKeyboards[0]
	top := kb.Keyboard[0][0]
	if top.WebApp == nil || top.WebApp.URL != "https://cr0.ir/admin/" {
		t.Fatalf("panel button = %+v", top)
	}
	if !kb.IsPersistent || !kb.ResizeKeyboard {
		t.Fatal("keyboard should be persistent and resized")
	}

	// Tapping the panel button sends its label as text: for admins that opens the Mini App.
	send(911, "📊 پنل مدیریت")
	last := e.last()
	if last.KB == nil || last.KB.InlineKeyboard[0][0].WebApp == nil {
		t.Fatalf("panel request should answer with a Mini App button, got %+v", last.KB)
	}

	// The report button (or plain media) continues the normal flow.
	send(912, "📝 ثبت گزارش باگ")
	if len(e.api.replyKeyboards) != 2 {
		t.Fatalf("report request should re-send the admin menu, keyboards = %d", len(e.api.replyKeyboards))
	}
}

func TestMediaIsReuploadedForStableFileIDs(t *testing.T) {
	e := newEnv(t)
	e.h.SetReportChat(-100123)
	e.text("/start")
	e.photo("photo-1")
	if len(e.api.uploads) != 1 || e.api.uploads[0] != "image:report.jpg" {
		t.Fatalf("media should be archived once, uploads = %v", e.api.uploads)
	}
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if len(d.Media) != 1 || d.Media[0].FileID != "fresh-p-1" {
		t.Fatalf("stored media should use the new file_id: %+v", d.Media)
	}
}

func TestMediaArchiveFailureKeepsOriginalIDs(t *testing.T) {
	e := newEnv(t) // no report chat configured
	e.text("/start")
	e.photo("photo-1")
	if len(e.api.uploads) != 0 {
		t.Fatalf("nothing should be uploaded, got %v", e.api.uploads)
	}
	d, _ := e.db.DraftFor(context.Background(), testUserID)
	if d.Media[0].FileID != "photo-1" {
		t.Fatalf("original file_id should be kept: %+v", d.Media)
	}
}

func TestConfirmTextWarnsAboutPrivateInfo(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	if !strings.Contains(e.last().Text, "اطلاعات شخصی") {
		t.Fatalf("confirmation should warn about private data: %q", e.last().Text)
	}
}

func TestAdminReplyIsOneMessageAndUserReplyNeedsNoCommand(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	e.callback("media:yes")
	e.callback("cat:0")
	e.callback("cat:yes")
	e.callback("desc:skip")
	e.callback("submit:yes")
	reports, err := e.db.ListReports(context.Background(), "")
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports = %v err = %v", reports, err)
	}
	id := reports[0].ID
	ctx := context.Background()

	if _, err := e.h.SendChat(ctx, id, "تیم کت", "سلام، مشکل را بررسی کردیم."); err != nil {
		t.Fatal(err)
	}
	if len(e.api.notifications) != 1 {
		t.Fatalf("admin message should produce exactly one Telegram message, got %d", len(e.api.notifications))
	}
	note := e.api.notifications[0]
	text, _ := note["text"].(string)
	if !strings.Contains(text, "گزارش «#") || !strings.Contains(text, "سلام، مشکل را بررسی کردیم.") {
		t.Fatalf("reply should name the report and carry the message: %q", text)
	}
	if !strings.Contains(text, "یک") {
		t.Fatalf("reply should ask for a single-message answer: %q", text)
	}

	// The user answers right away, without /start or any other command.
	before := len(e.api.sent)
	e.text("بله، هنوز همان مشکل را دارم")
	msgs, err := e.db.ChatMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[1].Sender != domain.SenderUser || msgs[1].Text != "بله، هنوز همان مشکل را دارم" {
		t.Fatalf("chat = %+v", msgs)
	}
	if got := len(e.api.sent) - before; got != 1 {
		t.Fatalf("acknowledgement should be one short message, got %d", got)
	}
}

func TestAfterChatEndsMessagesDoNotReopenIt(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	e.callback("media:yes")
	e.callback("cat:0")
	e.callback("cat:yes")
	e.callback("desc:skip")
	e.callback("submit:yes")
	reports, _ := e.db.ListReports(context.Background(), "")
	id := reports[0].ID
	ctx := context.Background()
	if _, err := e.h.SendChat(ctx, id, "تیم کت", "اولین پیام"); err != nil {
		t.Fatal(err)
	}
	e.text("/cancel")
	if got := e.db.ChatReport(ctx, testUserID); got != "" {
		t.Fatalf("after /cancel nothing should stay in chat mode, got %q", got)
	}

	// A later plain message must not silently revive the old conversation.
	e.text("سلام")
	msgs, _ := e.db.ChatMessages(ctx, id)
	if len(msgs) != 1 {
		t.Fatalf("old conversation should not grow after /cancel: %+v", msgs)
	}
	if !strings.Contains(e.last().Text, "گزارش فعالی وجود ندارد") {
		t.Fatalf("expected a no-report hint, got %q", e.last().Text)
	}
}

func TestReportStoresFullUserIdentity(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	e.photo("p1")
	e.callback("media:yes")
	e.callback("cat:0")
	e.callback("cat:yes")
	e.callback("desc:skip")
	e.callback("submit:yes")
	reports, err := e.db.ListReports(context.Background(), "")
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports = %v err = %v", reports, err)
	}
	u := reports[0].User
	if u.Username != "ali" || u.FirstName != "علی" || u.DisplayName() != "علی" {
		t.Fatalf("user identity not stored: %+v", u)
	}
}

func TestAdminReplyIsAnchoredToTheReportMedia(t *testing.T) {
	e := newEnv(t)
	e.text("/start")
	mediaID := e.api.nextUserID()
	e.photo("p1")
	e.callback("media:yes")
	e.callback("cat:0")
	e.callback("cat:yes")
	e.callback("desc:skip")
	e.callback("submit:yes")
	reports, _ := e.db.ListReports(context.Background(), "")
	id := reports[0].ID

	if _, err := e.h.SendChat(context.Background(), id, "تیم کت", "بررسی کردیم"); err != nil {
		t.Fatal(err)
	}
	note := e.api.notifications[0]
	if got, _ := note["reply_to_message_id"].(int64); got != mediaID {
		t.Fatalf("reply should be attached to the report media (%d), got %v", mediaID, note["reply_to_message_id"])
	}

}
