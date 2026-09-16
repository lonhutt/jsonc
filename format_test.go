package jsonc

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// applyEditsForTest is a local, deliberately simple applier so format tests do
// not depend on Task 12's ApplyEdits. Replace its body with a call to
// ApplyEdits once Task 12 lands.
func applyEditsForTest(text string, edits []Edit) (string, error) {
	out := text
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		out = out[:e.Offset] + e.Content + out[e.Offset+e.Length:]
	}
	return out, nil
}

func spaces2() FormatOptions {
	return FormatOptions{TabSize: 2, InsertSpaces: true, EOL: "\n"}
}

func tabs4() FormatOptions {
	return FormatOptions{TabSize: 4, InsertSpaces: false, EOL: "\n"}
}

// detect2 leaves EOL empty so the terminator is taken from the document.
func detect2() FormatOptions {
	return FormatOptions{TabSize: 2, InsertSpaces: true}
}

// editsEqual compares edit lists, treating a nil and an empty list as equal.
func editsEqual(a, b []Edit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isAllWhitespace(s string) bool {
	return strings.TrimLeft(s, " \t\r\n\v\f") == ""
}

// tokenSignature is the sequence of non-trivia tokens in text, each as its kind
// plus its exact source slice. Format is a whitespace-only rewriter, so this
// must be identical before and after formatting: any change means a token was
// destroyed, created, or merged with its neighbour -- a line comment that
// swallowed the next token, a deleted comment, or `1 2` collapsed into `12`.
func tokenSignature(text string) []string {
	var out []string
	s := NewScanner(text)
	for {
		k := s.Scan()
		if k == EOF {
			return out
		}
		if k == Trivia || k == LineBreak {
			continue
		}
		out = append(out, k.String()+":"+text[s.Offset():s.Offset()+s.Length()])
	}
}

// checkEditInvariants asserts the properties that make Format safe to apply to
// a live document, independent of what the edits happen to say:
// edits are ordered and non-overlapping, and every edit replaces whitespace
// with whitespace.
func checkEditInvariants(t *testing.T, in string, edits []Edit) {
	t.Helper()
	prevEnd := -1
	for i, e := range edits {
		if e.Offset < 0 || e.Length < 0 || e.Offset+e.Length > len(in) {
			t.Errorf("edit %d %+v out of bounds for %q (len %d)", i, e, in, len(in))
			continue
		}
		if e.Offset < prevEnd {
			t.Errorf("edit %d %+v overlaps or precedes the previous edit (ends at %d) in %q", i, e, prevEnd, in)
		}
		prevEnd = e.Offset + e.Length
		replaced := in[e.Offset : e.Offset+e.Length]
		if !isAllWhitespace(replaced) {
			t.Errorf("edit %d %+v in %q replaces non-whitespace %q", i, e, in, replaced)
		}
		if !isAllWhitespace(e.Content) {
			t.Errorf("edit %d %+v in %q inserts non-whitespace %q", i, e, in, e.Content)
		}
		if replaced == e.Content {
			t.Errorf("edit %d %+v in %q is a no-op: existing text already equals the content", i, e, in)
		}
	}
}

// assertFormat formats text, applies the edits, and checks the result -- plus
// the invariants that hold for every whole-document format: minimal edits,
// whitespace-only edits, token preservation, and idempotence.
func assertFormat(t *testing.T, in, want string, o FormatOptions) {
	t.Helper()
	edits := Format(in, nil, o)
	checkEditInvariants(t, in, edits)
	got, err := applyEditsForTest(in, edits)
	if err != nil {
		t.Fatalf("applying edits to %q: %v", in, err)
	}
	if got != want {
		t.Errorf("Format(%q):\n got %q\nwant %q\nedits %+v", in, got, want, edits)
		return
	}
	if before, after := tokenSignature(in), tokenSignature(got); !reflect.DeepEqual(before, after) {
		t.Errorf("Format(%q) changed the token sequence:\n before %q\n  after %q", in, before, after)
	}
	if again := Format(got, nil, o); len(again) != 0 {
		t.Errorf("Format(%q) is not idempotent: re-formatting %q produced %d edits: %+v", in, got, len(again), again)
	}
}

// --- whole-document formatting -------------------------------------------
//
// Every expectation below was produced by running the real
// node-jsonc-parser 3.3.1 format() over the same input and options.

func TestFormatObject(t *testing.T) {
	assertFormat(t, `{"a":1}`, "{\n  \"a\": 1\n}", spaces2())
	assertFormat(t, `{}`, `{}`, spaces2())
	assertFormat(t, `{"a":{"b":1}}`, "{\n  \"a\": {\n    \"b\": 1\n  }\n}", spaces2())
	assertFormat(t, `{"a":{"b":{"c":1}}}`, "{\n  \"a\": {\n    \"b\": {\n      \"c\": 1\n    }\n  }\n}", spaces2())
	assertFormat(t, `{"a":"x","b":"y"}`, "{\n  \"a\": \"x\",\n  \"b\": \"y\"\n}", spaces2())
	assertFormat(t, `{"a":1,}`, "{\n  \"a\": 1,\n}", spaces2())
	assertFormat(t, "{\n}", `{}`, spaces2())
}

func TestFormatArray(t *testing.T) {
	assertFormat(t, `[1,2]`, "[\n  1,\n  2\n]", spaces2())
	assertFormat(t, `[]`, `[]`, spaces2())
	assertFormat(t, `[[1]]`, "[\n  [\n    1\n  ]\n]", spaces2())
	assertFormat(t, `[{"a":1},{"b":2}]`, "[\n  {\n    \"a\": 1\n  },\n  {\n    \"b\": 2\n  }\n]", spaces2())
	assertFormat(t, "[\n]", `[]`, spaces2())
	assertFormat(t, `{"a":[1,{"b":null},true,false,"s"]}`,
		"{\n  \"a\": [\n    1,\n    {\n      \"b\": null\n    },\n    true,\n    false,\n    \"s\"\n  ]\n}", spaces2())
	assertFormat(t, "{\"a\":[\n1,\n2]}", "{\n  \"a\": [\n    1,\n    2\n  ]\n}", spaces2())
}

// TestFormatIndentLevelDecrements guards the nesting counter in both
// directions. An indentLevel that increments but never decrements, or one that
// decrements even when closing an empty container, gets these wrong.
func TestFormatIndentLevelDecrements(t *testing.T) {
	assertFormat(t, `{"a":{"b":{}}}`, "{\n  \"a\": {\n    \"b\": {}\n  }\n}", spaces2())
	assertFormat(t, `[[[]]]`, "[\n  [\n    []\n  ]\n]", spaces2())
	assertFormat(t, `[[1]]`, "[\n  [\n    1\n  ]\n]", spaces2())
}

func TestFormatCollapsesExistingWhitespace(t *testing.T) {
	assertFormat(t, "{\n\n\n  \"a\"   :    1   }", "{\n  \"a\": 1\n}", spaces2())
	assertFormat(t, "{\n\n  \"a\":1\n}", "{\n  \"a\": 1\n}", spaces2())
	assertFormat(t, "{\t\"a\"\t:\t1\t}", "{\n  \"a\": 1\n}", spaces2())
	assertFormat(t, "\n\n  {}", "{}", spaces2())
	assertFormat(t, "  \"x\"  ", "\"x\"", spaces2())
	assertFormat(t, "{\"a\":1}\n\n  ", "{\n  \"a\": 1\n}", spaces2())
}

func TestFormatDegenerateDocuments(t *testing.T) {
	assertFormat(t, ``, ``, spaces2())
	assertFormat(t, `   `, `   `, spaces2())
	assertFormat(t, `1`, `1`, spaces2())
	assertFormat(t, `}`, `}`, spaces2())
	// Three closers with nothing open drive indentLevel negative; the indent
	// string for a negative level is empty, not a panic.
	assertFormat(t, `]]]`, "]\n]\n]", spaces2())
}

func TestFormatTabs(t *testing.T) {
	assertFormat(t, `{"a":1}`, "{\n\t\"a\": 1\n}", tabs4())
	assertFormat(t, `{"a":{"b":1}}`, "{\n\t\"a\": {\n\t\t\"b\": 1\n\t}\n}", tabs4())
}

func TestFormatDefaultsTabSize(t *testing.T) {
	// TabSize 0 falls back to 4.
	o := FormatOptions{InsertSpaces: true, EOL: "\n"}
	assertFormat(t, `{"a":1}`, "{\n    \"a\": 1\n}", o)
}

func TestDefaultFormatOptions(t *testing.T) {
	o := DefaultFormatOptions()
	if o.TabSize != 4 || !o.InsertSpaces || o.EOL != "\n" || o.InsertFinalNewline || o.KeepLines {
		t.Errorf("DefaultFormatOptions() = %+v, want {4 true \"\\n\" false false}", o)
	}
}

func TestFormatMultibyteText(t *testing.T) {
	// Offsets are bytes, not runes: a multi-byte key must not shift an edit.
	assertFormat(t, "{\"é中\":1}", "{\n  \"é中\": 1\n}", spaces2())
	assertFormat(t, "{\"a\":\"😀\"}", "{\n  \"a\": \"😀\"\n}", spaces2())
	assertFormat(t, "{\"a\":\"ü\",\"b\":\"漢字\"}", "{\n  \"a\": \"ü\",\n  \"b\": \"漢字\"\n}", spaces2())
}

// --- minimality -----------------------------------------------------------

func TestFormatEmitsNoEditsWhenAlreadyFormatted(t *testing.T) {
	for _, in := range []string{
		"{\n  \"a\": 1\n}",
		"[\n  1,\n  2\n]",
		"{\n  \"a\": {\n    \"b\": 1\n  }\n}",
		"{\n  \"a\": 1 // c\n}",
		"{\n  \"a\": 1\n  // c\n}",
		"{ /* c */}",
		"{}",
	} {
		if edits := Format(in, nil, spaces2()); len(edits) != 0 {
			t.Errorf("already-formatted input %q produced %d edits, want 0: %+v", in, len(edits), edits)
		}
	}
	// Same, for a CRLF document with EOL detection.
	if edits := Format("{\r\n  \"a\": 1\r\n}", nil, detect2()); len(edits) != 0 {
		t.Errorf("already-formatted CRLF input produced %d edits, want 0: %+v", len(edits), edits)
	}
}

// TestFormatEditsOnlyTheGapsThatDiffer pins the exact edit list. A formatter
// that rewrote every gap -- or replaced the whole document -- would still
// produce the right text, so only this test can see the difference.
func TestFormatEditsOnlyTheGapsThatDiffer(t *testing.T) {
	tests := []struct {
		in   string
		want []Edit
	}{
		// One bad gap in an otherwise formatted document: one edit.
		{"{\n  \"a\":1\n}", []Edit{{Offset: 8, Length: 0, Content: " "}}},
		// Nothing but a missing space after the colon on the second line.
		{"{\n  \"a\": 1,\n  \"b\":2\n}", []Edit{{Offset: 18, Length: 0, Content: " "}}},
		// Every gap wrong: exactly three, no more.
		{`{"a":1}`, []Edit{
			{Offset: 1, Length: 0, Content: "\n  "},
			{Offset: 5, Length: 0, Content: " "},
			{Offset: 6, Length: 0, Content: "\n"},
		}},
		// Only the trailing whitespace is wrong.
		{"{\n  \"a\": 1\n}\n\n ", []Edit{{Offset: 12, Length: 3, Content: ""}}},
	}
	for _, tc := range tests {
		got := Format(tc.in, nil, spaces2())
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Format(%q) edits =\n %+v\nwant %+v", tc.in, got, tc.want)
		}
	}
}

// --- comments -------------------------------------------------------------

// TestFormatLineCommentKeepsItsNewline is the data-loss guard. In each case the
// gap after a line comment must stay a line break; collapsing it to a space
// would comment out everything that follows on the rewritten line.
func TestFormatLineCommentKeepsItsNewline(t *testing.T) {
	tests := []struct{ in, want string }{
		// After a colon: the value would be swallowed.
		{"{\"a\": // c\n1}", "{\n  \"a\": // c\n  1\n}"},
		// After a key: the colon and value would be swallowed.
		{"{\"a\" // c\n: 1}", "{\n  \"a\" // c\n  : 1\n}"},
		// After a value: the comma and the next element would be swallowed.
		{"[1 // c\n, 2]", "[\n  1 // c\n  ,\n  2\n]"},
		// After a comma: the next member would be swallowed.
		{"{\"a\":1,// c\n\"b\":2}", "{\n  \"a\": 1, // c\n  \"b\": 2\n}"},
		// After an opening brace.
		{"{// c\n\"a\":1}", "{ // c\n  \"a\": 1\n}"},
		// Two line comments in a row: the second must not be folded onto the first.
		{"{// a\n// b\n\"x\":1}", "{ // a\n  // b\n  \"x\": 1\n}"},
		{"{\"a\"://c\n//d\n1}", "{\n  \"a\": //c\n  //d\n  1\n}"},
		// A line comment as the last thing before a closer.
		{"{\"a\":1 // c\n}", "{\n  \"a\": 1 // c\n}"},
		{"{\"a\":{\"b\":1 // c\n}}", "{\n  \"a\": {\n    \"b\": 1 // c\n  }\n}"},
		{"[1,\n// c\n]", "[\n  1,\n  // c\n]"},
		// A line comment ending in CRLF keeps the CRLF.
		{"{\"a\":1 // c\r\n}", "{\r\n  \"a\": 1 // c\r\n}"},
	}
	for _, tc := range tests {
		o := spaces2()
		if strings.Contains(tc.in, "\r") {
			o = detect2()
		}
		assertFormat(t, tc.in, tc.want, o)
		got, _ := applyEditsForTest(tc.in, Format(tc.in, nil, o))
		assertNothingFollowsALineComment(t, tc.in, got)
	}
}

// assertNothingFollowsALineComment checks property 2 structurally, without
// reference to any expected string: in the formatted text, the token after a
// line comment must start on a later line. If it does not, the formatter has
// commented it out.
func assertNothingFollowsALineComment(t *testing.T, in, formatted string) {
	t.Helper()
	s := NewScanner(formatted)
	sawComment, commentLine := false, 0
	for {
		k := s.Scan()
		if k == EOF {
			return
		}
		if k == Trivia || k == LineBreak {
			continue
		}
		if sawComment && s.StartLine() == commentLine {
			t.Errorf("Format(%q) = %q: %v token at offset %d shares its line with the line comment above it",
				in, formatted, k, s.Offset())
		}
		sawComment = k == LineComment
		commentLine = s.StartLine()
	}
}

func TestFormatBlockComments(t *testing.T) {
	tests := []struct{ in, want string }{
		// A block comment on the same line as a value stays on that line,
		// separated by exactly one space.
		{`{"a":1/* c */}`, "{\n  \"a\": 1 /* c */\n}"},
		{`{"a":1   /* c */}`, "{\n  \"a\": 1 /* c */\n}"},
		// On its own line it stays on its own line, re-indented.
		{"{\"a\":1\n/* c */}", "{\n  \"a\": 1\n  /* c */\n}"},
		{"{\n/* c */\n\"a\":1}", "{\n  /* c */\n  \"a\": 1\n}"},
		{"{\n/* c */ \"a\":1}", "{\n  /* c */ \"a\": 1\n}"},
		// A block comment is not a value: an empty object stays empty.
		{`{ /* c */ }`, "{ /* c */}"},
		// Two block comments in a row are separated.
		{`[/*a*//*b*/]`, "[ /*a*/ /*b*/]"},
		// A multi-line block comment's own line breaks are left alone.
		{"{\"a\":/* x\ny */1}", "{\n  \"a\": /* x\ny */ 1\n}"},
		{"{\"a\":[1,// one\n2],\"b\":{/*x*/\"c\":true}}",
			"{\n  \"a\": [\n    1, // one\n    2\n  ],\n  \"b\": { /*x*/\n    \"c\": true\n  }\n}"},
		{"[\n  // head\n  {\"a\":1}\n]", "[\n  // head\n  {\n    \"a\": 1\n  }\n]"},
	}
	for _, tc := range tests {
		assertFormat(t, tc.in, tc.want, spaces2())
	}
}

func TestFormatCommentOnlyDocument(t *testing.T) {
	assertFormat(t, "// c", "// c", spaces2())
	assertFormat(t, "/* c */", "/* c */", spaces2())
}

// --- malformed input ------------------------------------------------------

// TestFormatMalformedInputIsNotCorrupted covers the cases where upstream sets
// its hasError flag and suppresses the edit. Without that suppression the
// whitespace between two adjacent values is deleted and `[1 2]` silently
// becomes `[12]` -- a different number.
func TestFormatMalformedInputIsNotCorrupted(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`[1 2]`, "[\n  1 2\n]"},
		{`[true false]`, "[\n  true false\n]"},
		{`{"a":@}`, "{\n  \"a\":@\n}"},
		// An unrecognized token is not a value either: the space after it
		// stays, or `@ 1` would be glued into one token.
		{`[@ 1]`, "[@ 1\n]"},
		{`{"a":@ 1}`, "{\n  \"a\":@ 1\n}"},
		{`[@,1]`, "[@,\n  1\n]"},
		{"{\"a\":1/* x", "{\n  \"a\": 1/* x"},
		{`{"a":"x}`, "{\n  \"a\":\"x}"},
		{`{"a":1}}`, "{\n  \"a\": 1\n}\n}"},
	}
	for _, tc := range tests {
		assertFormat(t, tc.in, tc.want, spaces2())
	}
}

// --- EOL ------------------------------------------------------------------

func TestFormatEOLDetection(t *testing.T) {
	// Empty EOL means: detect from the document.
	assertFormat(t, "{\r\n\"a\":1}", "{\r\n  \"a\": 1\r\n}", detect2())
	// A lone CR document stays a lone CR document.
	assertFormat(t, "{\r\"a\":1}", "{\r  \"a\": 1\r}", detect2())
	// With no terminator in the document at all, fall back to the option.
	assertFormat(t, `{"a":1}`, "{\n  \"a\": 1\n}", detect2())
	assertFormat(t, `{"a":1}`, "{\r\n  \"a\": 1\r\n}", FormatOptions{TabSize: 2, InsertSpaces: true, EOL: "\r\n"})
	// ...and with neither option nor terminator, \n.
	assertFormat(t, `{"a":1}`, "{\n  \"a\": 1\n}", FormatOptions{TabSize: 2, InsertSpaces: true})
	assertFormat(t, "{\r\n// c\r\n\"a\":1\r\n}", "{\r\n  // c\r\n  \"a\": 1\r\n}", detect2())
}

// TestFormatDocumentEOLWinsOverOption pins upstream's precedence: the
// terminator already in the document wins, and the EOL option is only a
// fallback for a document that has none. Formatting a CRLF file with EOL "\n"
// must not rewrite it to LF.
func TestFormatDocumentEOLWinsOverOption(t *testing.T) {
	assertFormat(t, "{\r\n\"a\":1}", "{\r\n  \"a\": 1\r\n}", spaces2())
	assertFormat(t, "{\r\"a\":1}", "{\r  \"a\": 1\r}", spaces2())
}

// --- ranges ---------------------------------------------------------------

func TestFormatRange(t *testing.T) {
	tests := []struct {
		name string
		in   string
		r    Range
		o    FormatOptions
		want string
		// edits, where they are worth pinning exactly.
		edits []Edit
	}{
		{
			// The initial indent comes from the text before the range, so the
			// nested object indents to 4, not to 2.
			name: "nested block keeps its depth",
			in:   "{\n  \"a\": {\"b\":1}\n}",
			r:    Range{Offset: 9, Length: 7},
			o:    spaces2(),
			want: "{\n  \"a\": {\n    \"b\": 1\n  }\n}",
			edits: []Edit{
				{Offset: 10, Length: 0, Content: "\n    "},
				{Offset: 14, Length: 0, Content: " "},
				{Offset: 15, Length: 0, Content: "\n  "},
			},
		},
		{
			name: "text outside the range is untouched",
			in:   "{\"x\":1,\n  \"b\"  :  2,\n  \"c\":3}",
			r:    Range{Offset: 10, Length: 10},
			o:    spaces2(),
			want: "{\"x\":1,\n  \"b\": 2,\n  \"c\":3}",
			edits: []Edit{
				{Offset: 13, Length: 2, Content: ""},
				{Offset: 16, Length: 2, Content: " "},
			},
		},
		{
			// The initial indent is a tab, counted as TabSize columns.
			name: "tab indentation before the range",
			in:   "{\n\t\"a\": {\"b\":1}\n}",
			r:    Range{Offset: 7, Length: 7},
			o:    tabs4(),
			want: "{\n\t\"a\": {\n\t\t\"b\": 1}\n}",
			edits: []Edit{
				{Offset: 9, Length: 0, Content: "\n\t\t"},
				{Offset: 13, Length: 0, Content: " "},
			},
		},
		{
			// The leading whitespace of the range's first line is rewritten to
			// the initial indent it implies -- three spaces at TabSize 2 is
			// level 1, which is two spaces.
			name:  "leading indentation is normalized",
			in:    "{\n   \"a\":1\n}",
			r:     Range{Offset: 2, Length: 8},
			o:     spaces2(),
			want:  "{\n  \"a\": 1\n}",
			edits: []Edit{{Offset: 2, Length: 3, Content: "  "}, {Offset: 9, Length: 0, Content: " "}},
		},
		{
			name:  "leading tab is normalized to the indent unit",
			in:    "{\n\t\"a\":1\n}",
			r:     Range{Offset: 2, Length: 8},
			o:     spaces2(),
			want:  "{\n  \"a\": 1\n}",
			edits: []Edit{{Offset: 2, Length: 1, Content: "  "}, {Offset: 7, Length: 0, Content: " "}},
		},
		{
			name:  "leading indentation rounds down to whole units",
			in:    "{\n     \"a\":1\n}",
			r:     Range{Offset: 2, Length: 10},
			o:     spaces2(),
			want:  "{\n    \"a\": 1\n}",
			edits: []Edit{{Offset: 2, Length: 5, Content: "    "}, {Offset: 11, Length: 0, Content: " "}},
		},
		{
			name:  "range covering the whole document",
			in:    `{"a":1}`,
			r:     Range{Offset: 0, Length: 7},
			o:     spaces2(),
			want:  "{\n  \"a\": 1\n}",
			edits: nil,
		},
		{
			name:  "empty range produces nothing",
			in:    `{"a":1}`,
			r:     Range{Offset: 3, Length: 0},
			o:     spaces2(),
			want:  `{"a":1}`,
			edits: []Edit{},
		},
		{
			// An insertion exactly at a range boundary is outside the range.
			name:  "insertions at the boundary are excluded",
			in:    `[1,2,3]`,
			r:     Range{Offset: 2, Length: 3},
			o:     spaces2(),
			want:  "[1,\n  2,3]",
			edits: []Edit{{Offset: 3, Length: 0, Content: "\n  "}},
		},
		{
			name:  "range inside one line",
			in:    "{\n  \"a\":1,\n  \"b\":2\n}",
			r:     Range{Offset: 5, Length: 5},
			o:     spaces2(),
			want:  "{\n  \"a\": 1,\n  \"b\":2\n}",
			edits: []Edit{{Offset: 8, Length: 0, Content: " "}},
		},
		{
			name:  "range crossing a line",
			in:    "{\n  \"a\":1,\n  \"b\":2\n}",
			r:     Range{Offset: 5, Length: 12},
			o:     spaces2(),
			want:  "{\n  \"a\": 1,\n  \"b\":2\n}",
			edits: []Edit{{Offset: 8, Length: 0, Content: " "}},
		},
		{
			name:  "range covering only the last token",
			in:    `{"a":1}`,
			r:     Range{Offset: 6, Length: 1},
			o:     spaces2(),
			want:  `{"a":1}`,
			edits: []Edit{},
		},
		{
			name:  "range running past the end of the document",
			in:    `{"a":1}`,
			r:     Range{Offset: 6, Length: 5},
			o:     spaces2(),
			want:  `{"a":1}`,
			edits: []Edit{},
		},
		{
			// The edit that trims trailing whitespace must stop at the end of
			// the range's last line, not run to the end of the document.
			name: "trailing text after the range survives",
			in:   "{\"a\":1}  \nTAIL",
			r:    Range{Offset: 0, Length: 9},
			o:    spaces2(),
			want: "{\n  \"a\": 1\n}\nTAIL",
			edits: []Edit{
				{Offset: 1, Length: 0, Content: "\n  "},
				{Offset: 5, Length: 0, Content: " "},
				{Offset: 6, Length: 0, Content: "\n"},
				{Offset: 7, Length: 2, Content: ""},
			},
		},
		{
			name:  "text before and after the range survives",
			in:    "x\n{\"a\":1}\n  junk",
			r:     Range{Offset: 2, Length: 7},
			o:     spaces2(),
			want:  "x\n{\n  \"a\": 1\n}\n  junk",
			edits: nil,
		},
		{
			name:  "a line comment inside the range keeps its break",
			in:    "{\n  \"a\":1 // c\n  ,\"b\":2\n}",
			r:     Range{Offset: 4, Length: 12},
			o:     spaces2(),
			want:  "{\n  \"a\": 1 // c\n  ,\"b\":2\n}",
			edits: []Edit{{Offset: 8, Length: 0, Content: " "}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.r
			edits := Format(tc.in, &r, tc.o)
			checkEditInvariants(t, tc.in, edits)
			got, err := applyEditsForTest(tc.in, edits)
			if err != nil {
				t.Fatalf("applying edits: %v", err)
			}
			if got != tc.want {
				t.Errorf("Format(%q, %+v):\n got %q\nwant %q\nedits %+v", tc.in, tc.r, got, tc.want, edits)
			}
			if tc.edits != nil && !editsEqual(edits, tc.edits) {
				t.Errorf("Format(%q, %+v) edits =\n %+v\nwant %+v", tc.in, tc.r, edits, tc.edits)
			}
			if before, after := tokenSignature(tc.in), tokenSignature(got); !reflect.DeepEqual(before, after) {
				t.Errorf("Format(%q, %+v) changed the token sequence:\n before %q\n  after %q", tc.in, tc.r, before, after)
			}
		})
	}
}

// TestFormatRangeEditsOverlapTheRange is the structural version of the range
// tests. An edit may cover the whole whitespace gap it belongs to, so it can
// reach a little past the requested range -- but every edit must overlap the
// range, and (via checkEditInvariants) every byte it touches is whitespace.
// A formatter that ignored the range would fail this for most offsets.
func TestFormatRangeEditsOverlapTheRange(t *testing.T) {
	in := "{\n  \"a\":1,\n  \"b\"  :2,\n  \"c\":3\n}"
	for off := 0; off <= len(in); off++ {
		for _, length := range []int{0, 1, 5, 12} {
			if off+length > len(in) {
				continue
			}
			r := Range{Offset: off, Length: length}
			edits := Format(in, &r, spaces2())
			checkEditInvariants(t, in, edits)
			for _, e := range edits {
				if !(e.Offset < r.Offset+r.Length && e.Offset+e.Length > r.Offset) {
					t.Fatalf("range %+v produced edit %+v that does not overlap it", r, e)
				}
			}
			got, _ := applyEditsForTest(in, edits)
			if before, after := tokenSignature(in), tokenSignature(got); !reflect.DeepEqual(before, after) {
				t.Fatalf("range %+v changed the token sequence: %q\n before %q\n after %q", r, got, before, after)
			}
		}
	}
}

// TestFormatNilRangeIsNotTheWholeDocumentRange pins an asymmetry that a caller
// would reasonably assume away. The nil form formats from indent level 0; the
// range form reads its initial indent from the leading whitespace of the
// range's first line, so a document that starts indented comes back one level
// deeper. Both outputs below are node-jsonc-parser's. The split is upstream's
// own `if (range)` branch, so this is deliberate, not incidental.
func TestFormatNilRangeIsNotTheWholeDocumentRange(t *testing.T) {
	in := `     {"a":1}`

	whole, _ := applyEditsForTest(in, Format(in, nil, spaces2()))
	if want := "{\n  \"a\": 1\n}"; whole != want {
		t.Errorf("nil range: got %q, want %q", whole, want)
	}

	r := Range{Offset: 0, Length: len(in)}
	ranged, _ := applyEditsForTest(in, Format(in, &r, spaces2()))
	if want := "    {\n      \"a\": 1\n    }"; ranged != want {
		t.Errorf("Range{0, len}: got %q, want %q", ranged, want)
	}

	if whole == ranged {
		t.Error("the two forms agree now; if that is intended, this test and Format's doc comment both need updating")
	}
}

func TestFormatRangeOutOfBoundsDoesNotPanic(t *testing.T) {
	in := `{"a":1}`
	for _, r := range []Range{
		{Offset: -5, Length: 3},
		{Offset: 0, Length: -1},
		{Offset: 100, Length: 3},
		{Offset: 3, Length: 1000},
		{Offset: -10, Length: 1000},
		{Offset: len(in), Length: 0},
		// Length that overflows when added to Offset: clamping the wrapped
		// sum would collapse the range and silently return no edits.
		{Offset: 0, Length: math.MaxInt},
		{Offset: 3, Length: math.MaxInt},
		{Offset: math.MaxInt, Length: math.MaxInt},
		{Offset: math.MinInt, Length: math.MaxInt},
		{Offset: math.MinInt, Length: math.MinInt},
	} {
		rr := r
		edits := Format(in, &rr, spaces2())
		checkEditInvariants(t, in, edits)
		if _, err := applyEditsForTest(in, edits); err != nil {
			t.Errorf("range %+v: applying edits failed: %v", r, err)
		}
	}

	// A length that runs past the end means "to the end" -- including when
	// Offset+Length overflows, which it does for any non-zero offset with
	// MaxInt. Clamping the wrapped sum instead of the length turns that into a
	// silent empty range and zero edits, so check from several offsets.
	for _, off := range []int{0, 1, 3, len(in)} {
		want := Format(in, &Range{Offset: off, Length: len(in) - off}, spaces2())
		for _, length := range []int{len(in) + 50, math.MaxInt} {
			got := Format(in, &Range{Offset: off, Length: length}, spaces2())
			if !editsEqual(got, want) {
				t.Errorf("Range{%d, %d} should reach the end of the document:\n got %+v\nwant %+v", off, length, got, want)
			}
		}
	}
}

// TestFormatEditsAreStructurallyValid checks what an applier needs, over
// documents and ranges chosen to be hostile rather than realistic. Task 12's
// ApplyEdits rejects overlapping edits, and the golden corpus only compares
// applied TEXT -- so an overlapping pair that happened to apply correctly here
// would pass every other test in this file and fail in Task 12.
func TestFormatEditsAreStructurallyValid(t *testing.T) {
	deep := func(open, close string, n int) string {
		return strings.Repeat(open, n) + strings.Repeat(close, n)
	}
	docs := map[string]string{
		"unbalanced closers":  "]]]]]]",
		"unbalanced openers":  "{{{{",
		"mixed closers":       "}]}]}]",
		"200-deep objects":    deep(`{"a":`, "}", 200),
		"200-deep arrays":     deep("[", "]", 200),
		"200 closers":         strings.Repeat("]", 200),
		"empty":               "",
		"whitespace only":     " \t \n\r\n \v\f ",
		"line breaks only":    "\n\n\n",
		"line comment only":   "// c",
		"block comment only":  "/* c */",
		"unterminated string": `{"a":"x`,
		"unterminated block":  "{/* x",
		"multibyte":           `{"é中":"漢字😀"}`,
		"multibyte comment":   "{// 中\n\"é\":1}",
		"commas only":         ",,,,",
		"colons only":         "::::",
		"crlf nested":         "{\r\n\"a\":[\r\n1,\r\n2]\r\n}",
		"lone cr nested":      "{\r\"a\":[\r1,\r2]\r}",
		"realistic":           "{\n  // c\n  \"a\": [1, 2, {\"b\": null}],\n  \"c\": \"d\" /* x */\n}",
	}
	options := map[string]FormatOptions{"spaces2": spaces2(), "tabs4": tabs4(), "zero": {}}

	for docName, in := range docs {
		rs := []*Range{
			nil,
			{Offset: 0, Length: 0},
			{Offset: 0, Length: len(in)},
			{Offset: -1, Length: 5},
			{Offset: -100, Length: 1},
			{Offset: len(in) + 10, Length: 5},
			{Offset: max(len(in)-1, 0), Length: 100},
			{Offset: 2, Length: math.MaxInt},
			{Offset: 0, Length: -1},
		}
		for off := 0; off <= len(in); off += 7 {
			rs = append(rs, &Range{Offset: off, Length: 11})
		}
		for optName, o := range options {
			for _, r := range rs {
				var rr *Range
				if r != nil {
					c := *r
					rr = &c
				}
				edits := Format(in, rr, o)

				prevEnd, prevOffset := 0, -1
				for i, e := range edits {
					where := func() string {
						return docName + "/" + optName + " range " + rangeString(r) + " edit " + itoa(i)
					}
					if e.Length < 0 {
						t.Errorf("%s: negative length: %+v", where(), e)
					}
					if e.Offset < 0 || e.Offset+e.Length > len(in) {
						t.Errorf("%s: out of bounds for a %d-byte document: %+v", where(), len(in), e)
						continue
					}
					if e.Offset < prevEnd {
						t.Errorf("%s: overlaps the previous edit, which ended at %d: %+v", where(), prevEnd, e)
					}
					if e.Offset == prevOffset {
						t.Errorf("%s: shares an offset with the previous edit: %+v", where(), e)
					}
					prevEnd, prevOffset = e.Offset+e.Length, e.Offset
				}

				got, err := applyEditsForTest(in, edits)
				if err != nil {
					t.Fatalf("%s/%s range %s: applying edits: %v", docName, optName, rangeString(r), err)
				}
				if before, after := tokenSignature(in), tokenSignature(got); !reflect.DeepEqual(before, after) {
					t.Errorf("%s/%s range %s: token sequence changed -> %q", docName, optName, rangeString(r), got)
				}
			}
		}
	}
}

func rangeString(r *Range) string {
	if r == nil {
		return "nil"
	}
	return "{" + itoa(r.Offset) + "," + itoa(r.Length) + "}"
}

func itoa(i int) string { return strconv.Itoa(i) }

// --- Task 11 options are accepted and ignored ----------------------------

func TestFormatIgnoresKeepLinesAndInsertFinalNewline(t *testing.T) {
	base := spaces2()
	keep := base
	keep.KeepLines = true
	final := base
	final.InsertFinalNewline = true

	in := "{\n\n  \"a\":1,\n\n  \"b\":2\n}\n"
	want := Format(in, nil, base)
	for name, o := range map[string]FormatOptions{"KeepLines": keep, "InsertFinalNewline": final} {
		if got := Format(in, nil, o); !reflect.DeepEqual(got, want) {
			t.Errorf("%s is implemented in task 10 but must be ignored until task 11:\n got %+v\nwant %+v", name, got, want)
		}
	}
	// Without InsertFinalNewline, trailing whitespace at the end of the
	// document is removed -- including a final newline.
	assertFormat(t, "{\"a\":1}\n", "{\n  \"a\": 1\n}", spaces2())
}

// --- properties -----------------------------------------------------------

// formatCorpus is the input set for the property test. It deliberately mixes
// line endings, comment placements, malformed fragments and multi-byte text.
var formatCorpus = []string{
	`{"a":1}`, `{}`, `[]`, `{"a":{"b":1}}`, `[1,2]`, `[[1]]`,
	"{\n\n\n  \"a\"   :    1   }", `{"a":{"b":{}}}`, `[[[]]]`,
	`{"a":1/* c */}`, `{"a":1   /* c */}`, "{// c\n\"a\":1}", "{\n// c\n\"a\":1}",
	"{\"a\":1 // c\n}", "{\"a\":1,// c\n\"b\":2}", "{\"a\": // c\n1}", "{\"a\" // c\n: 1}",
	"[1 // c\n, 2]", "{\"a\":1\n// c\n}", "{\"a\":1\n/* c */}", "{\n/* c */ \"a\":1}",
	"{\n/* c */\n\"a\":1}", `{ /* c */ }`, `[1 2]`, `{"a":@}`, "\n\n  {}", "{\"a\":1}\n\n  ",
	"", "   ", "{\r\n\"a\":1}", "{\r\"a\":1}",
	`{"a":[1,{"b":null},true,false,"s"]}`, `}`, `]]]`, `{"a":1,}`,
	"{\n  \"a\": 1\n}", `{"a":"x","b":"y"}`, `{"a" "b"}`,
	"{\r\n// c\r\n\"a\":1\r\n}", "{\"a\":[\n1,\n2]}", "{\n}", "[\n]", "// c", "// c\n", "1",
	"  \"x\"  ", "{\"a\":[1,// one\n2],\"b\":{/*x*/\"c\":true}}", "[1,\n// c\n]",
	"{\"a\":/* x\ny */1}", "{\"a\":{\"b\":1 // c\n}}", "{// a\n// b\n\"x\":1}",
	"{\"a\":1 // c\r\n}", "{\"é中\":1}", "{\"a\":\"😀\"}", "{\"a\":1}\n", "{\n\n  \"a\":1\n}",
	"{\"a\":1\n// c\n}\n", "{\t\"a\"\t:\t1\t}", "[\n  // head\n  {\"a\":1}\n]",
	`{"a":{"b":{"c":1}}}`, `[{"a":1},{"b":2}]`, `[1,,2]`, `{"a":}`, `{:1}`, `{"a":1}}`,
	`[1]]`, `{"a":tru}`, `{"a":-}`, `{"a":1e}`, `[/*a*//*b*/]`, "{\"a\"://c\n//d\n1}",
	"{\"a\": \"line\\nbreak\\tin\\\\string\"}", "{\"a\":\"//not a comment\"}",
	"{\"a\":\"/* not a comment */\"}", "[1,2,3,4,5,6,7,8,9,10]",
	"{\r\n  \"a\": [\r\n    1\r\n  ]\r\n}",
}

// TestFormatProperties asserts, over the whole corpus and several option sets,
// the four things that must hold no matter what the output looks like:
// whitespace-only minimal edits, idempotence, token preservation, and value
// preservation.
func TestFormatProperties(t *testing.T) {
	optionSets := map[string]FormatOptions{
		"spaces2":  spaces2(),
		"tabs4":    tabs4(),
		"detect":   detect2(),
		"zero":     {},
		"spaces8":  {TabSize: 8, InsertSpaces: true, EOL: "\n"},
		"crlfOpt":  {TabSize: 2, InsertSpaces: true, EOL: "\r\n"},
		"defaults": DefaultFormatOptions(),
	}
	for name, o := range optionSets {
		for _, in := range formatCorpus {
			t.Run(name+"/"+strings.NewReplacer("\n", "\\n", "\r", "\\r", "\t", "\\t", "/", "_").Replace(in), func(t *testing.T) {
				edits := Format(in, nil, o)
				checkEditInvariants(t, in, edits)
				once, err := applyEditsForTest(in, edits)
				if err != nil {
					t.Fatalf("applying edits: %v", err)
				}
				// Idempotence.
				if again := Format(once, nil, o); len(again) != 0 {
					twice, _ := applyEditsForTest(once, again)
					t.Errorf("not idempotent:\n once %q\ntwice %q\nedits %+v", once, twice, again)
				}
				// Token preservation.
				if before, after := tokenSignature(in), tokenSignature(once); !reflect.DeepEqual(before, after) {
					t.Errorf("token sequence changed:\n in  %q -> %q\n before %q\n after  %q", in, once, before, after)
				}
				// Value preservation, for the inputs that parse cleanly.
				if v, errs := Parse(in); len(errs) == 0 {
					w, werrs := Parse(once)
					if len(werrs) != 0 {
						t.Errorf("formatted text no longer parses: %q -> %q: %v", in, once, werrs)
					} else if !reflect.DeepEqual(v, w) {
						t.Errorf("value changed: %q -> %q\n before %#v\n after  %#v", in, once, v, w)
					}
				}
			})
		}
	}
}

// --- golden corpus --------------------------------------------------------

// goldenCase is one line of testdata/format-golden.jsonl.
type goldenCase struct {
	Opts    string `json:"opts"`
	Range   string `json:"range"`
	Text    string `json:"text"`
	Applied string `json:"applied"`
	Edits   []Edit `json:"edits"`
}

// goldenOptions and goldenRanges must stay in step with the tables in
// testdata/gen_format_golden.js; the names are what the golden file records.
var goldenOptions = map[string]FormatOptions{
	"spaces2":  {TabSize: 2, InsertSpaces: true, EOL: "\n"},
	"tabs4":    {TabSize: 4, InsertSpaces: false, EOL: "\n"},
	"detect":   {TabSize: 2, InsertSpaces: true, EOL: ""},
	"zero":     {TabSize: 0, InsertSpaces: false, EOL: ""},
	"spaces8":  {TabSize: 8, InsertSpaces: true, EOL: "\n"},
	"crlfOpt":  {TabSize: 2, InsertSpaces: true, EOL: "\r\n"},
	"defaults": {TabSize: 4, InsertSpaces: true, EOL: "\n"},
}

var goldenRanges = map[string]*Range{
	"nil":      nil,
	"head":     {Offset: 0, Length: 3},
	"mid":      {Offset: 2, Length: 5},
	"empty":    {Offset: 1, Length: 0},
	"past-end": {Offset: 3, Length: 100},
	"indent":   {Offset: 4, Length: 9},
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

// TestFormatGoldenCorpus replays the recorded output of the real
// node-jsonc-parser for every case in testdata/format-golden.jsonl.
//
// The rest of this file pins a few dozen strings by hand and otherwise checks
// structural properties -- whitespace-only, idempotent, token-preserving --
// which hold for any sane whitespace rewriter, not specifically for upstream's.
// This test is the one that fails when we drift from upstream's exact answers.
// It needs no node: the answers are frozen in the file. Regenerate it with
// testdata/gen_format_golden.js when pinning a new upstream version, and read
// the diff -- every changed line is a behavior change.
func TestFormatGoldenCorpus(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "format-golden.jsonl"))
	if err != nil {
		t.Fatalf("opening the golden corpus: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)

	if !sc.Scan() {
		t.Fatal("the golden corpus is empty")
	}
	var header struct {
		Upstream  string `json:"upstream"`
		Generator string `json:"generator"`
		Cases     int    `json:"cases"`
	}
	if err := json.Unmarshal(sc.Bytes(), &header); err != nil {
		t.Fatalf("parsing the golden corpus header: %v", err)
	}
	if header.Upstream == "" || header.Cases == 0 {
		t.Fatalf("golden corpus header looks wrong: %+v", header)
	}

	failures, n := 0, 0
	for line := 2; sc.Scan(); line++ {
		var c goldenCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("line %d: %v", line, err)
		}
		o, ok := goldenOptions[c.Opts]
		if !ok {
			t.Fatalf("line %d: unknown option set %q -- keep this table in step with the generator", line, c.Opts)
		}
		r, ok := goldenRanges[c.Range]
		if !ok {
			t.Fatalf("line %d: unknown range shape %q -- keep this table in step with the generator", line, c.Range)
		}
		n++

		var rr *Range
		if r != nil {
			copyOfR := *r
			rr = &copyOfR
		}
		edits := Format(c.Text, rr, o)
		checkEditInvariants(t, c.Text, edits)
		got, err := applyEditsForTest(c.Text, edits)
		if err != nil {
			t.Fatalf("line %d: applying edits: %v", line, err)
		}

		bad := got != c.Applied
		// Upstream's offsets are UTF-16 code units and ours are bytes, so the
		// recorded edit list is only comparable for ASCII documents. The
		// generator records it exactly then, and its absence there is a bug in
		// the corpus, not a case to skip.
		if isASCII(c.Text) {
			if c.Edits == nil {
				t.Fatalf("line %d: ASCII case with no recorded edits: %+v", line, c)
			}
			if !editsEqual(edits, c.Edits) {
				bad = true
			}
		}
		if bad {
			failures++
			if failures <= 10 {
				t.Errorf("golden line %d (upstream %s, opts %s, range %s)\n  text  %q\n   got  %q\n  want  %q\n  got edits  %+v\n  want edits %+v",
					line, header.Upstream, c.Opts, c.Range, c.Text, got, c.Applied, edits, c.Edits)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the golden corpus: %v", err)
	}
	if n != header.Cases {
		t.Errorf("golden corpus is truncated: read %d cases, header says %d", n, header.Cases)
	}
	if failures > 10 {
		t.Errorf("%d golden cases differ from upstream %s (first 10 shown)", failures, header.Upstream)
	}
	t.Logf("replayed %d cases recorded from node-jsonc-parser %s", n, header.Upstream)
}

// TestFormatPropertiesForRanges runs the same structural checks for every
// range of a handful of documents.
func TestFormatPropertiesForRanges(t *testing.T) {
	docs := []string{
		"{\n  \"a\":1,\n  \"b\": {\"c\":2}\n}",
		"[\n1, // c\n2]",
		"{\r\n\"a\":1\r\n}",
	}
	for _, in := range docs {
		for off := 0; off <= len(in); off++ {
			for length := 0; off+length <= len(in); length += 3 {
				r := Range{Offset: off, Length: length}
				edits := Format(in, &r, spaces2())
				checkEditInvariants(t, in, edits)
				got, err := applyEditsForTest(in, edits)
				if err != nil {
					t.Fatalf("%q %+v: %v", in, r, err)
				}
				if before, after := tokenSignature(in), tokenSignature(got); !reflect.DeepEqual(before, after) {
					t.Fatalf("%q %+v: token sequence changed -> %q\n before %q\n after %q", in, r, got, before, after)
				}
			}
		}
	}
}
