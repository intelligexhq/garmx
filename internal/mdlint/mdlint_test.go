package mdlint

import "testing"

// TestNormalize exercises each structural rule and the interactions between
// them via before/after fixtures.
func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "already canonical is unchanged",
			in:   "# Title\n\nBody paragraph.\n",
			want: "# Title\n\nBody paragraph.\n",
		},
		{
			name: "blank lines around heading",
			in:   "Intro line.\n## Section\nBody.\n",
			want: "Intro line.\n\n## Section\n\nBody.\n",
		},
		{
			name: "blank lines around list",
			in:   "Lead in:\n- one\n- two\nAfter the list.\n",
			want: "Lead in:\n\n- one\n- two\n\nAfter the list.\n",
		},
		{
			name: "nested and multiline list items stay together",
			in:   "Text:\n\n- top\n  continued line\n  - nested\n\nDone.\n",
			want: "Text:\n\n- top\n  continued line\n  - nested\n\nDone.\n",
		},
		{
			name: "ordered list detected",
			in:   "Steps:\n1. first\n2. second\nEnd.\n",
			want: "Steps:\n\n1. first\n2. second\n\nEnd.\n",
		},
		{
			name: "fence gets a language and surrounding blanks",
			in:   "Before.\n```\ndiagram\n```\nAfter.\n",
			want: "Before.\n\n```text\ndiagram\n```\n\nAfter.\n",
		},
		{
			name: "existing fence language preserved",
			in:   "```go\nx := 1\n```\n",
			want: "```go\nx := 1\n```\n",
		},
		{
			name: "code fence content preserved verbatim",
			in:   "```text\n# not a heading\n\n\nblank kept\n- not a list\n```\n",
			want: "```text\n# not a heading\n\n\nblank kept\n- not a list\n```\n",
		},
		{
			name: "trailing whitespace stripped outside code",
			in:   "line with spaces   \n",
			want: "line with spaces\n",
		},
		{
			name: "consecutive blanks collapsed",
			in:   "a\n\n\n\nb\n",
			want: "a\n\nb\n",
		},
		{
			name: "leading and trailing blanks trimmed, final newline added",
			in:   "\n\nonly line",
			want: "only line\n",
		},
		{
			name: "crlf normalized to lf",
			in:   "a\r\n\r\nb\r\n",
			want: "a\n\nb\n",
		},
		{
			name: "heading directly before list gets one blank",
			in:   "## Head\n- item\n",
			want: "## Head\n\n- item\n",
		},
		{
			name: "hash without space is not a heading",
			in:   "#hashtag stays inline\n",
			want: "#hashtag stays inline\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(Normalize([]byte(tt.in)))
			if got != tt.want {
				t.Errorf("Normalize() mismatch\n in:   %q\n got:  %q\n want: %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeIdempotent verifies that normalizing an already-normalized
// document is a no-op, which is what lets the CLI use equality as its check.
func TestNormalizeIdempotent(t *testing.T) {
	inputs := []string{
		"# Title\n\nBody.\n",
		"Intro line.\n## Section\nBody.\n",
		"Lead in:\n- one\n- two\nAfter.\n",
		"Before.\n```\ndiagram\n```\nAfter.\n",
		"a\n\n\n\nb\n",
	}
	for _, in := range inputs {
		once := Normalize([]byte(in))
		twice := Normalize(once)
		if string(once) != string(twice) {
			t.Errorf("not idempotent for %q\n once:  %q\n twice: %q", in, once, twice)
		}
	}
}
