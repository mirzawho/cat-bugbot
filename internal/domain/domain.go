package domain

import "time"

type State string

const (
	StateAwaitingMedia       State = "awaiting_media"
	StateConfirmingMedia     State = "confirming_media"
	StateSelectingCategory   State = "selecting_category"
	StateConfirmingCategory  State = "confirming_category"
	StateAwaitingDescription State = "awaiting_description"
	StateConfirmingSubmit    State = "confirming_submission"
	// StateChatting means an admin opened the chat for the user's report; the
	// user's text goes to the report conversation instead of a new report.
	StateChatting State = "chatting"
)

type ReportStatus string

const (
	StatusNew      ReportStatus = "new"
	StatusReview   ReportStatus = "review"
	StatusResolved ReportStatus = "resolved"
	StatusRejected ReportStatus = "rejected"
)

func (s ReportStatus) Valid() bool {
	switch s {
	case StatusNew, StatusReview, StatusResolved, StatusRejected:
		return true
	}
	return false
}

// CanMoveTo is the admin workflow: a new report is picked up for review, then
// accepted (resolved). Resolved/rejected are final; rejecting is allowed
// straight from a new report by the store.
func (s ReportStatus) CanMoveTo(next ReportStatus) bool {
	switch s {
	case StatusNew:
		return next == StatusReview
	case StatusReview:
		return next == StatusResolved || next == StatusRejected
	}
	return false
}

type MediaType string

const (
	MediaImage MediaType = "image"
	MediaVideo MediaType = "video"
)

type Media struct {
	Type         MediaType `json:"type"`
	FileID       string    `json:"file_id"`
	FileUniqueID string    `json:"file_unique_id"`
}

type UserInfo struct {
	ID        int64
	Username  string
	FirstName string
	LastName  string
}

func (u UserInfo) DisplayName() string {
	switch {
	case u.FirstName != "" && u.LastName != "":
		return u.FirstName + " " + u.LastName
	case u.FirstName != "":
		return u.FirstName
	default:
		return u.Username
	}
}

type Draft struct {
	User     UserInfo
	State    State
	Media    []Media
	Category string
	Desc     string
	// ReportID is set while the user is chatting with the team about this report.
	ReportID string
	// MediaMessageID is the chat message that carries the reported media.
	MediaMessageID int64
	// PromptMessageID is the chat message that holds the current step's prompt;
	// the flow edits it in place instead of sending a new one.
	PromptMessageID int64
	UpdatedAt       time.Time
}

type Report struct {
	ID          string
	User        UserInfo
	Media       []Media
	Category    string
	Description string
	Status      ReportStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ResolvedAt  *time.Time
	RejectedAt  *time.Time
}

type ChatMessage struct {
	ID        int64
	ReportID  string
	Sender    Sender
	AdminID   int64
	AdminName string
	Text      string
	CreatedAt time.Time
}

type Sender string

const (
	SenderUser  Sender = "user"
	SenderAdmin Sender = "admin"
)
