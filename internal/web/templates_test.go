package web

import (
	"os"
	"strings"
	"testing"
)

// The templates and scripts are user-facing; a typo here is invisible to the Go compiler.
func TestTemplatesRender(t *testing.T) {
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "login.html"} {
		var b strings.Builder
		if err := tmpl.ExecuteTemplate(&b, name, map[string]any{"AdminName": "مدیر", "Error": ""}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := b.String()
		for _, want := range []string{"chat-sheet", "app.js", "app.css"} {
			if name == "index.html" && !strings.Contains(out, want) {
				t.Fatalf("%s is missing %q", name, want)
			}
		}
		if strings.Contains(out, "{{") {
			t.Fatalf("%s has unrendered template actions", name)
		}
	}
}

func TestPanelScriptHasSheetAndFullWidthActions(t *testing.T) {
	js, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"openChatSheet", "closeChatSheet", "btn-full", "first_name", "last_name"} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("app.js is missing %q", want)
		}
	}
}
