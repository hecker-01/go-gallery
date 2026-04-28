package textutil

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		s, suffix string
		max       int
		want      string
	}{
		{"hello world", "…", 5, "hell…"},
		{"hello", "…", 10, "hello"},
		{"hello world", "...", 8, "hello..."},
		{"", "…", 5, ""},
		{"abc", "…", 3, "abc"},
	}
	for _, tt := range tests {
		got := Truncate(tt.s, tt.suffix, tt.max)
		if got != tt.want {
			t.Errorf("Truncate(%q, %q, %d) = %q, want %q", tt.s, tt.suffix, tt.max, got, tt.want)
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		s, sep, want string
	}{
		{"Hello World!", "-", "hello-world"},
		{"go gallery", "_", "go_gallery"},
		{"  multiple   spaces  ", "-", "multiple-spaces"},
		{"123", "-", "123"},
	}
	for _, tt := range tests {
		got := Slugify(tt.s, tt.sep)
		if got != tt.want {
			t.Errorf("Slugify(%q, %q) = %q, want %q", tt.s, tt.sep, got, tt.want)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name, repl, want string
	}{
		{"valid_name.jpg", "_", "valid_name.jpg"},
		{"bad/path", "_", "bad_path"},
		{"a:b*c?d", "_", "a_b_c_d"},
		{"with\\backslash", "_", "with_backslash"},
	}
	for _, tt := range tests {
		got := SanitizeFilename(tt.name, tt.repl)
		if got != tt.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCollapseSpaces(t *testing.T) {
	tests := []struct{ s, want string }{
		{"  hello   world  ", "hello world"},
		{"single", "single"},
		{"  ", ""},
		{"a  b  c", "a b c"},
	}
	for _, tt := range tests {
		got := CollapseSpaces(tt.s)
		if got != tt.want {
			t.Errorf("CollapseSpaces(%q) = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestTruncateWords(t *testing.T) {
	got := TruncateWords("one two three four five", "...", 3)
	want := "one two three..."
	if got != want {
		t.Errorf("TruncateWords: got %q, want %q", got, want)
	}
	// No truncation needed.
	got = TruncateWords("one two", "...", 5)
	if got != "one two" {
		t.Errorf("TruncateWords no-trunc: got %q, want %q", got, "one two")
	}
}

func TestContainsFold(t *testing.T) {
	if !ContainsFold("Hello World", "HELLO") {
		t.Error("ContainsFold should find case-insensitive match")
	}
	if ContainsFold("Hello World", "xyz") {
		t.Error("ContainsFold should return false for missing substring")
	}
}
