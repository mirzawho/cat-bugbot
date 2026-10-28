package telegram

import (
	"context"
	"errors"
	"testing"
)

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		desc        string
		modified    bool
		notEditable bool
	}{
		{"Bad Request: message is not modified: specified new message content and reply markup are exactly the same", true, false},
		{"Bad Request: message to edit not found", false, true},
		{"Bad Request: message can't be edited", false, true},
		{"Bad Request: there is no caption in the message to edit", false, true},
		{"Bad Request: chat not found", false, false},
		{"Forbidden: bot was blocked by the user", false, false},
	}
	for _, c := range cases {
		e := &Error{Method: "editMessageText", Code: 400, Description: c.desc}
		if e.IsNotModified() != c.modified || e.IsNotEditable() != c.notEditable {
			t.Errorf("%q: modified=%v notEditable=%v", c.desc, e.IsNotModified(), e.IsNotEditable())
		}
	}
}

func TestCallReturnsTypedError(t *testing.T) {
	// Unreachable host: the transport error must surface, not a typed API error.
	c := NewClientWithBase("http://127.0.0.1:1", "token")
	err := c.EditMessageText(context.Background(), 1, 2, "x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		t.Fatalf("transport error misclassified as API error: %v", apiErr)
	}
}

func TestGetFileMapsInvalidFileID(t *testing.T) {
	e := &Error{Method: "getFile", Code: 400, Description: "Bad Request: invalid file_id"}
	if !e.IsFileIDInvalid() {
		t.Fatal("400 invalid file_id should be recognised")
	}
	other := &Error{Method: "getFile", Code: 404, Description: "Not Found"}
	if other.IsFileIDInvalid() {
		t.Fatal("404 must not be treated as invalid file_id")
	}
	if ErrFileIDInvalid == nil {
		t.Fatal("sentinel missing")
	}
}
