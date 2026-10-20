package dashboard

import "testing"

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{
		"export-user-portal": "EU",
		"mesra":              "ME",
		"sfExcelReporter":    "SE",
		"acme_web.api":       "AW",
		"x":                  "X",
		"":                   "?",
	} {
		if got := initials(in); got != want {
			t.Errorf("initials(%q) = %q, want %q", in, got, want)
		}
	}
}
