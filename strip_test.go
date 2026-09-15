package jsonc

import (
	"strings"
	"testing"
)

func TestStripComments(t *testing.T) {
	tests := []struct{ in, want string }{
		{`{"a":1}`, `{"a":1}`},
		{"{\n// c\n\"a\":1}", "{\n\n\"a\":1}"},
		{`{/* c */"a":1}`, `{"a":1}`},
		{"{\"a\":1} // trailing", "{\"a\":1} "},
		// A // inside a string is not a comment.
		{`{"a":"http://x"}`, `{"a":"http://x"}`},
		{`{"a":"/* not a comment */"}`, `{"a":"/* not a comment */"}`},
		// A multi-line block comment collapses but keeps its line breaks.
		{"{/* a\nb */}", "{\n}"},
	}
	for _, tc := range tests {
		if got := StripComments(tc.in); got != tc.want {
			t.Errorf("StripComments(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReplaceComments(t *testing.T) {
	tests := []struct {
		in   string
		ch   rune
		want string
	}{
		{`{/*c*/}`, ' ', `{     }`},
		{"{// c\n}", ' ', "{    \n}"},
		{`{"a":1}`, ' ', `{"a":1}`},
	}
	for _, tc := range tests {
		got := ReplaceComments(tc.in, tc.ch)
		if got != tc.want {
			t.Errorf("ReplaceComments(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if len(got) != len(tc.in) {
			t.Errorf("ReplaceComments(%q) changed length %d -> %d; offsets must be preserved",
				tc.in, len(tc.in), len(got))
		}
	}
}

func TestReplaceCommentsPreservesLineBreaks(t *testing.T) {
	in := "{/* a\nb */}"
	got := ReplaceComments(in, ' ')
	if len(got) != len(in) {
		t.Fatalf("length changed %d -> %d", len(in), len(got))
	}
	if got[5] != '\n' {
		t.Errorf("line break at offset 5 was replaced; got %q", got[5])
	}
}

func TestStripCommentsCRLF(t *testing.T) {
	// A CRLF inside a block comment must survive as BOTH bytes, or every line
	// number after the comment shifts and the document's line endings become
	// inconsistent.
	in := "{/* a\r\nb */}"
	got := StripComments(in)
	want := "{\r\n}"
	if got != want {
		t.Errorf("StripComments(%q) = %q, want %q", in, got, want)
	}
}

func TestReplaceCommentsCRLF(t *testing.T) {
	in := "{/* a\r\nb */}"
	got := ReplaceComments(in, ' ')
	if len(got) != len(in) {
		t.Fatalf("ReplaceComments(%q) length %d, want %d", in, len(got), len(in))
	}
	if !strings.Contains(got, "\r\n") {
		t.Errorf("ReplaceComments(%q) = %q, lost the CRLF inside the comment", in, got)
	}
	// Both bytes must survive independently: a \r that got replaced while the
	// \n survived would still contain "\n" but not "\r\n".
	if strings.Count(got, "\r") != 1 || strings.Count(got, "\n") != 1 {
		t.Errorf("ReplaceComments(%q) = %q, want exactly one \\r and one \\n", in, got)
	}
}

func TestStrippedOutputParses(t *testing.T) {
	in := "{\n // c\n \"a\": 1 /* b */\n}"
	stripped := StripComments(in)
	if _, errs := Parse(stripped, DisallowComments()); len(errs) != 0 {
		t.Errorf("stripped output %q still has comments: %v", stripped, errs)
	}
}
