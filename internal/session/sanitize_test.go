package session

import "testing"

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"notes":               "notes",
		"my-project":          "my_project",
		"3.6-form-software":   "3_6_form_software",
		"already_fine_99":     "already_fine_99",
		"UPPER-Case":          "UPPER_Case",
		"spaces and (parens)": "spaces_and__parens_",
		"src/my-project":      "src_my_project",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeNameIsIdempotent(t *testing.T) {
	// bloom sanitizes whatever --session hands it. A caller that computes a
	// session name and sanitizes it first has to land on the same string, or
	// the two disagree about which session to attach to.
	for _, in := range []string{"src/my-project", "a.b.c", "TICKET-123"} {
		once := SanitizeName(in)
		if twice := SanitizeName(once); twice != once {
			t.Errorf("SanitizeName not idempotent for %q: %q -> %q", in, once, twice)
		}
	}
}
