package config

import "testing"

func TestTelegramAPIBase(t *testing.T) {
	cases := map[string]string{
		"":                              "https://api.telegram.org",
		"https://cr0.ir":                "https://cr0.ir",
		"https://cr0.ir/":               "https://cr0.ir",
		"http://127.0.0.1:9000":         "http://127.0.0.1:9000",
		"  https://api.telegram.org/  ": "https://api.telegram.org",
	}
	for raw, want := range cases {
		if got := telegramAPIBase(raw); got != want {
			t.Errorf("telegramAPIBase(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestWarnAPIBaseWithPath(t *testing.T) {
	if w := warnAPIBase("https://cr0.ir", "https://cr0.ir"); w != "" {
		t.Fatalf("no warning expected: %s", w)
	}
	if w := warnAPIBase("https://cr0.ir/telegram", "https://cr0.ir/telegram"); w == "" {
		t.Fatal("expected warning for a base with a path")
	}
}

func TestParseIDs(t *testing.T) {
	ids, err := parseIDs(`"1780243209", 42 42`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 1780243209 || ids[1] != 42 {
		t.Fatalf("ids = %v", ids)
	}
	if _, err := parseIDs("abc"); err == nil {
		t.Fatal("expected invalid id error")
	}
}
