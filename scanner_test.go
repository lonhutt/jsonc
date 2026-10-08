package jsonc

import (
	"reflect"
	"testing"
)

// assertKinds scans text to EOF and checks the token kinds; upstream's
// assertKinds.
func assertKinds(t *testing.T, text string, want ...SyntaxKind) {
	t.Helper()
	s := NewScanner(text)
	var got []SyntaxKind
	for k := s.Scan(); k != EOF; k = s.Scan() {
		got = append(got, k)
	}
	if len(got) != len(want) {
		t.Fatalf("scan(%q) produced %d tokens %v, want %d %v", text, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("scan(%q) token %d = %v, want %v", text, i, got[i], want[i])
		}
	}
}

func TestScanStructural(t *testing.T) {
	assertKinds(t, "{", OpenBrace)
	assertKinds(t, "}", CloseBrace)
	assertKinds(t, "[", OpenBracket)
	assertKinds(t, "]", CloseBracket)
	assertKinds(t, ":", Colon)
	assertKinds(t, ",", Comma)
	assertKinds(t, "{}", OpenBrace, CloseBrace)
	assertKinds(t, "[]", OpenBracket, CloseBracket)
	assertKinds(t, " \t ", Trivia)
	assertKinds(t, "\n", LineBreak)
	assertKinds(t, "\r\n", LineBreak)
	assertKinds(t, "{ }", OpenBrace, Trivia, CloseBrace)
	assertKinds(t, "@", Unknown)
}

func TestScanOffsetsAndLengths(t *testing.T) {
	s := NewScanner("{ }")
	if k := s.Scan(); k != OpenBrace || s.Offset() != 0 || s.Length() != 1 {
		t.Fatalf("token 1 = %v at %d len %d, want OpenBrace at 0 len 1", k, s.Offset(), s.Length())
	}
	if k := s.Scan(); k != Trivia || s.Offset() != 1 || s.Length() != 1 {
		t.Fatalf("token 2 = %v at %d len %d, want Trivia at 1 len 1", k, s.Offset(), s.Length())
	}
	if k := s.Scan(); k != CloseBrace || s.Offset() != 2 || s.Position() != 3 {
		t.Fatalf("token 3 = %v at %d pos %d, want CloseBrace at 2 pos 3", k, s.Offset(), s.Position())
	}
	if k := s.Scan(); k != EOF {
		t.Fatalf("token 4 = %v, want EOF", k)
	}
}

func TestScanLineTracking(t *testing.T) {
	// columns are bytes, lines 0-based
	s := NewScanner("{\n\"é\":1}")
	if k := s.Scan(); k != OpenBrace || s.StartLine() != 0 || s.StartColumn() != 0 {
		t.Fatalf("token 1 = %v line %d col %d, want OpenBrace 0,0", k, s.StartLine(), s.StartColumn())
	}
	if k := s.Scan(); k != LineBreak || s.StartLine() != 0 || s.StartColumn() != 1 {
		t.Fatalf("token 2 = %v line %d col %d, want LineBreak 0,1", k, s.StartLine(), s.StartColumn())
	}
	s.Scan()
	if s.StartLine() != 1 || s.StartColumn() != 0 {
		t.Fatalf("token 3 line %d col %d, want 1,0", s.StartLine(), s.StartColumn())
	}
}

func TestIgnoreTriviaOption(t *testing.T) {
	s := NewScanner("{ \n }", IgnoreTrivia())
	if k := s.Scan(); k != OpenBrace {
		t.Fatalf("token 1 = %v, want OpenBrace", k)
	}
	if k := s.Scan(); k != CloseBrace {
		t.Fatalf("token 2 = %v, want CloseBrace (trivia should be skipped)", k)
	}
}

func TestSetPosition(t *testing.T) {
	s := NewScanner("{}[]")
	s.SetPosition(2)
	if k := s.Scan(); k != OpenBracket || s.Offset() != 2 {
		t.Fatalf("after SetPosition(2): %v at %d, want OpenBracket at 2", k, s.Offset())
	}
}

func TestSetPositionResetsLineTracking(t *testing.T) {
	// jumping back mustn't leave stale line state; columns can't go negative
	s := NewScanner("{\n}[]")
	s.Scan()
	s.Scan()
	s.Scan()
	s.SetPosition(0)
	if k := s.Scan(); k != OpenBrace {
		t.Fatalf("token = %v, want OpenBrace", k)
	}
	if s.StartLine() != 0 || s.StartColumn() != 0 {
		t.Errorf("after SetPosition(0): line %d col %d, want 0,0", s.StartLine(), s.StartColumn())
	}

	s2 := NewScanner("{\n\n  []")
	s2.SetPosition(5)
	// before the next Scan, StartColumn is the jump target's
	if col := s2.StartColumn(); col < 0 {
		t.Errorf("after SetPosition(5), before Scan: StartColumn() = %d, want >= 0", col)
	}
	if k := s2.Scan(); k != OpenBracket {
		t.Fatalf("token = %v, want OpenBracket", k)
	}
	if s2.StartLine() != 2 || s2.StartColumn() != 2 {
		t.Errorf("after SetPosition(5): line %d col %d, want 2,2", s2.StartLine(), s2.StartColumn())
	}

	// \r\n is one line break across a jump
	s3 := NewScanner("{\r\n[]")
	s3.SetPosition(3)
	s3.Scan()
	if s3.StartLine() != 1 || s3.StartColumn() != 0 {
		t.Errorf("after SetPosition(3) over CRLF: line %d col %d, want 1,0", s3.StartLine(), s3.StartColumn())
	}
}

func TestSetPositionNegative(t *testing.T) {
	// used to panic on the next Scan
	for _, pos := range []int{-5, -1} {
		s := NewScanner("{}")
		s.SetPosition(pos)
		if col := s.StartColumn(); col < 0 {
			t.Errorf("SetPosition(%d): StartColumn() = %d, want >= 0", pos, col)
		}
		if k := s.Scan(); k != OpenBrace || s.Offset() != 0 {
			t.Errorf("SetPosition(%d): %v at %d, want OpenBrace at 0", pos, k, s.Offset())
		}
	}
}

func assertValue(t *testing.T, text string, wantKind SyntaxKind, wantValue string, wantErr ScanError) {
	t.Helper()
	s := NewScanner(text)
	k := s.Scan()
	if k != wantKind {
		t.Fatalf("scan(%q) kind = %v, want %v", text, k, wantKind)
	}
	if s.Value() != wantValue {
		t.Errorf("scan(%q) value = %q, want %q", text, s.Value(), wantValue)
	}
	if s.TokenError() != wantErr {
		t.Errorf("scan(%q) error = %v, want %v", text, s.TokenError(), wantErr)
	}
}

func TestScanStrings(t *testing.T) {
	assertValue(t, `"hello"`, StringLiteral, "hello", ScanNone)
	assertValue(t, `""`, StringLiteral, "", ScanNone)
	assertValue(t, `"\""`, StringLiteral, `"`, ScanNone)
	assertValue(t, `"\\"`, StringLiteral, `\`, ScanNone)
	assertValue(t, `"\/"`, StringLiteral, "/", ScanNone)
	assertValue(t, `"\b\f\n\r\t"`, StringLiteral, "\b\f\n\r\t", ScanNone)
	assertValue(t, `"é"`, StringLiteral, "é", ScanNone)
	// a surrogate pair decodes to one rune
	assertValue(t, `"😀"`, StringLiteral, "😀", ScanNone)
	assertValue(t, `"héllo"`, StringLiteral, "héllo", ScanNone)
}

func TestScanStringErrors(t *testing.T) {
	assertValue(t, `"unterminated`, StringLiteral, "unterminated", ScanUnexpectedEndOfString)
	assertValue(t, "\"broken\nrest\"", StringLiteral, "broken", ScanUnexpectedEndOfString)
	// a bad escape adds nothing, as upstream
	assertValue(t, `"\q"`, StringLiteral, "", ScanInvalidEscapeCharacter)
	assertValue(t, `"\u00"`, StringLiteral, "", ScanInvalidUnicode)
	// a lone high surrogate gives U+FFFD and an error, not a panic
	assertValue(t, `"\ud83d"`, StringLiteral, "�", ScanInvalidUnicode)
}

func TestScanNumbers(t *testing.T) {
	assertValue(t, "0", NumericLiteral, "0", ScanNone)
	assertValue(t, "42", NumericLiteral, "42", ScanNone)
	assertValue(t, "-1", NumericLiteral, "-1", ScanNone)
	assertValue(t, "1.5", NumericLiteral, "1.5", ScanNone)
	assertValue(t, "1e10", NumericLiteral, "1e10", ScanNone)
	assertValue(t, "-1.5e-10", NumericLiteral, "-1.5e-10", ScanNone)
	assertValue(t, "1E+2", NumericLiteral, "1E+2", ScanNone)
	// Value is the raw text, not a normalized number
	assertValue(t, "1.50", NumericLiteral, "1.50", ScanNone)
}

func TestScanNumberErrors(t *testing.T) {
	// the bad exponent is left out of the value, as upstream
	assertValue(t, "1e", NumericLiteral, "1", ScanUnexpectedEndOfNumber)
	assertValue(t, "1e+", NumericLiteral, "1", ScanUnexpectedEndOfNumber)
	assertValue(t, "1.", NumericLiteral, "1.", ScanUnexpectedEndOfNumber)
	assertValue(t, "-", Unknown, "-", ScanNone)
	assertValue(t, "-x", Unknown, "-", ScanNone)
}

func TestScanKeywords(t *testing.T) {
	assertValue(t, "true", TrueKeyword, "true", ScanNone)
	assertValue(t, "false", FalseKeyword, "false", ScanNone)
	assertValue(t, "null", NullKeyword, "null", ScanNone)
	// an unknown word is one token and no scan error; the parser reports it
	assertValue(t, "tru", Unknown, "tru", ScanNone)
	assertValue(t, "undefined", Unknown, "undefined", ScanNone)
	assertValue(t, "\f", Unknown, "\f", ScanNone)
}

func TestScanLiteralSequence(t *testing.T) {
	assertKinds(t, `{"a":1}`, OpenBrace, StringLiteral, Colon, NumericLiteral, CloseBrace)
	assertKinds(t, `[true,false,null]`,
		OpenBracket, TrueKeyword, Comma, FalseKeyword, Comma, NullKeyword, CloseBracket)
}

func TestScanComments(t *testing.T) {
	assertValue(t, "// line", LineComment, "// line", ScanNone)
	assertValue(t, "//", LineComment, "//", ScanNone)
	assertValue(t, "/* block */", BlockComment, "/* block */", ScanNone)
	assertValue(t, "/**/", BlockComment, "/**/", ScanNone)
	// a line comment stops before the line break
	assertKinds(t, "// c\n{", LineComment, LineBreak, OpenBrace)
	assertKinds(t, "/* a */{", BlockComment, OpenBrace)
	assertKinds(t, "/* a\nb */{", BlockComment, OpenBrace)
}

func TestScanCommentErrors(t *testing.T) {
	assertValue(t, "/* unterminated", BlockComment, "/* unterminated", ScanUnexpectedEndOfComment)
	assertValue(t, "/", Unknown, "/", ScanNone)
	assertValue(t, "/x", Unknown, "/", ScanNone)
}

func TestBlockCommentLineTracking(t *testing.T) {
	s := NewScanner("/* a\nb */\n{")
	if k := s.Scan(); k != BlockComment {
		t.Fatalf("token 1 = %v, want BlockComment", k)
	}
	s.Scan() // LineBreak after the comment
	if k := s.Scan(); k != OpenBrace {
		t.Fatalf("token 3 = %v, want OpenBrace", k)
	}
	// one break inside the comment, one after it
	if s.StartLine() != 2 {
		t.Errorf("OpenBrace on line %d, want 2", s.StartLine())
	}
	if s.StartColumn() != 0 {
		t.Errorf("OpenBrace at column %d, want 0", s.StartColumn())
	}
}

func TestIgnoreTriviaSkipsComments(t *testing.T) {
	s := NewScanner("{ // c\n }", IgnoreTrivia())
	if k := s.Scan(); k != OpenBrace {
		t.Fatalf("token 1 = %v, want OpenBrace", k)
	}
	if k := s.Scan(); k != CloseBrace {
		t.Fatalf("token 2 = %v, want CloseBrace", k)
	}
}

func BenchmarkScanPlainString(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := NewScanner(`"hello world"`)
		s.Scan()
		_ = s.Value()
	}
}

// Regression: text between two escapes was dropped; the pending run was only
// flushed when the builder was first allocated.
func TestScanStringMultipleEscapes(t *testing.T) {
	tests := []struct{ in, want string }{
		{`"a\tb\tc"`, "a\tb\tc"},
		{`"one\ttwo\tthree\tfour"`, "one\ttwo\tthree\tfour"},
		{`"x\ny\nz"`, "x\ny\nz"},
		{`"\\a\\b"`, `\a\b`},
		{`"a\"b\"c"`, `a"b"c`},
		// \u escapes go through a different writeEscape path
		{`"a\u0042b\u0043c"`, "aBbCc"},
		{`"a\tb\u00e9"`, "a\tbé"},
		// Adjacent escapes leave empty runs, so this passes even with the flush
		// bug; it's a control, not coverage.
		{`"\u0041\u0042\u0043"`, "ABC"},
		// escapes at the very start and the very end
		{`"\ta\tb"`, "\ta\tb"},
		{`"a\tb\t"`, "a\tb\t"},
	}
	for _, tc := range tests {
		s := NewScanner(tc.in)
		if k := s.Scan(); k != StringLiteral {
			t.Errorf("Scan(%s) = %v, want StringLiteral", tc.in, k)
			continue
		}
		if got := s.Value(); got != tc.want {
			t.Errorf("Scan(%s).Value() = %q, want %q", tc.in, got, tc.want)
		}
		if s.TokenError() != ScanNone {
			t.Errorf("Scan(%s) error = %v, want ScanNone", tc.in, s.TokenError())
		}
	}
}

// assertUpstreamKinds is upstream's assertKinds: the kinds in order, each with
// no scan error.
func assertUpstreamKinds(t *testing.T, text string, want ...SyntaxKind) {
	t.Helper()
	s := NewScanner(text)
	var got []SyntaxKind
	for k := s.Scan(); k != EOF; k = s.Scan() {
		got = append(got, k)
		if s.TokenError() != ScanNone {
			t.Errorf("%q: %v at %d has %v", text, k, s.Offset(), s.TokenError())
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q: got %v, want %v", text, got, want)
	}
}

// assertScanError is upstream's: the first token has err, and the kinds follow.
func assertScanError(t *testing.T, text string, err ScanError, want ...SyntaxKind) {
	t.Helper()
	s := NewScanner(text)
	var got []SyntaxKind
	for k := s.Scan(); k != EOF; k = s.Scan() {
		if len(got) == 0 && s.TokenError() != err {
			t.Errorf("%q: first token has %v, want %v", text, s.TokenError(), err)
		}
		got = append(got, k)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q: got %v, want %v", text, got, want)
	}
}

// Port of upstream's "tokens".
func TestUpstreamTokens(t *testing.T) {
	assertUpstreamKinds(t, "{", OpenBrace)
	assertUpstreamKinds(t, "}", CloseBrace)
	assertUpstreamKinds(t, "[", OpenBracket)
	assertUpstreamKinds(t, "]", CloseBracket)
	assertUpstreamKinds(t, ":", Colon)
	assertUpstreamKinds(t, ",", Comma)
}

// Port of upstream's "comments".
func TestUpstreamComments(t *testing.T) {
	assertUpstreamKinds(t, "// this is a comment", LineComment)
	assertUpstreamKinds(t, "// this is a comment\n", LineComment, LineBreak)
	assertUpstreamKinds(t, "/* this is a comment*/", BlockComment)
	assertUpstreamKinds(t, "/* this is a \r\ncomment*/", BlockComment)
	assertUpstreamKinds(t, "/* this is a \ncomment*/", BlockComment)

	assertScanError(t, "/* this is a", ScanUnexpectedEndOfComment, BlockComment)
	assertScanError(t, "/* this is a \ncomment", ScanUnexpectedEndOfComment, BlockComment)

	assertUpstreamKinds(t, "/ ttt", Unknown, Trivia, Unknown)
}

// Port of upstream's "strings".
func TestUpstreamStrings(t *testing.T) {
	for _, in := range []string{`"test"`, `"\""`, `"\/"`, `"\b"`, `"\f"`, `"\n"`, `"\r"`, `"\t"`, "\"\u88ff\"", "\"\u200b\u2028\""} {
		assertUpstreamKinds(t, in, StringLiteral)
	}
	assertScanError(t, `"\v"`, ScanInvalidEscapeCharacter, StringLiteral)

	assertScanError(t, `"test`, ScanUnexpectedEndOfString, StringLiteral)
	assertScanError(t, "\"test\n\"", ScanUnexpectedEndOfString, StringLiteral, LineBreak, StringLiteral)

	assertScanError(t, "\"\t\"", ScanInvalidCharacter, StringLiteral)
	assertScanError(t, "\"\t \"", ScanInvalidCharacter, StringLiteral)
	assertScanError(t, "\"\x00 \"", ScanInvalidCharacter, StringLiteral)
}

// Port of upstream's "numbers".
func TestUpstreamNumbers(t *testing.T) {
	for _, in := range []string{"0", "0.1", "-0.1", "-1", "1", "123456789", "10", "90", "90E+123", "90e+123", "90e-123", "90E-123", "90E123", "90e123"} {
		assertUpstreamKinds(t, in, NumericLiteral)
	}
	assertUpstreamKinds(t, "01", NumericLiteral, NumericLiteral)
	assertUpstreamKinds(t, "-01", NumericLiteral, NumericLiteral)

	assertUpstreamKinds(t, "-", Unknown)
	assertUpstreamKinds(t, ".0", Unknown)
}

// Port of upstream's "keywords: true, false, null".
func TestUpstreamKeywords(t *testing.T) {
	assertUpstreamKinds(t, "true", TrueKeyword)
	assertUpstreamKinds(t, "false", FalseKeyword)
	assertUpstreamKinds(t, "null", NullKeyword)
	assertUpstreamKinds(t, "true false null", TrueKeyword, Trivia, FalseKeyword, Trivia, NullKeyword)

	assertUpstreamKinds(t, "nulllll", Unknown)
	assertUpstreamKinds(t, "True", Unknown)
	assertUpstreamKinds(t, "foo-bar", Unknown)
	assertUpstreamKinds(t, "foo bar", Unknown, Trivia, Unknown)

	assertUpstreamKinds(t, "false//hello", FalseKeyword, LineComment)
}

// Port of upstream's "trivia".
func TestUpstreamTrivia(t *testing.T) {
	assertUpstreamKinds(t, " ", Trivia)
	assertUpstreamKinds(t, "  \t  ", Trivia)
	assertUpstreamKinds(t, "  \t  \n  \t  ", Trivia, LineBreak, Trivia)
	assertUpstreamKinds(t, "\r\n", LineBreak)
	assertUpstreamKinds(t, "\r", LineBreak)
	assertUpstreamKinds(t, "\n", LineBreak)
	assertUpstreamKinds(t, "\n\r", LineBreak, LineBreak)
	assertUpstreamKinds(t, "\n   \n", LineBreak, Trivia, LineBreak)
}

// A bad escape adds nothing to the value, as upstream, and takes a whole
// character, so the value stays valid UTF-8.
func TestScanBadEscapesAddNothing(t *testing.T) {
	assertValue(t, "\"\\é\"", StringLiteral, "", ScanInvalidEscapeCharacter)
	assertValue(t, `"a\qb"`, StringLiteral, "ab", ScanInvalidEscapeCharacter)
	assertValue(t, `"a\u12g"`, StringLiteral, "ag", ScanInvalidUnicode)
}

// An escaped line break continues the string onto the next line; later
// tokens have to report that line. Upstream doesn't count it.
func TestScanEscapedLineBreakCountsTheLine(t *testing.T) {
	for _, in := range []string{"[\"a\\\n\", \n x]", "[\"a\\\r\n\", \n x]"} {
		s := NewScanner(in)
		for k := s.Scan(); k != EOF; k = s.Scan() {
			if s.Value() == "x" && (s.StartLine() != 2 || s.StartColumn() != 1) {
				t.Errorf("%q: x at line %d, column %d; want 2, 1", in, s.StartLine(), s.StartColumn())
			}
		}
	}
}
