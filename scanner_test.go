package jsonc

import "testing"

// assertKinds scans text to EOF and compares the token sequence.
// Ported from upstream's assertKinds helper in json.test.ts.
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
	// Byte columns, 0-based lines. Token 3 is Unknown until Task 2 implements
	// strings, so this asserts only line and column, which hold either way.
	s := NewScanner("{\n\"é\":1}")
	if k := s.Scan(); k != OpenBrace || s.StartLine() != 0 || s.StartColumn() != 0 {
		t.Fatalf("token 1 = %v line %d col %d, want OpenBrace 0,0", k, s.StartLine(), s.StartColumn())
	}
	if k := s.Scan(); k != LineBreak || s.StartLine() != 0 || s.StartColumn() != 1 {
		t.Fatalf("token 2 = %v line %d col %d, want LineBreak 0,1", k, s.StartLine(), s.StartColumn())
	}
	// After the line break the scanner must report line 1, column 0.
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
	// Jumping BACKWARD must not leave stale line state behind. A column is a
	// byte count from the start of a line, so it can never be negative.
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

	// Jumping FORWARD past a line break must report the real line.
	s2 := NewScanner("{\n\n  []")
	s2.SetPosition(5)
	// Before the next Scan, StartColumn reflects the jump target itself and
	// must never be negative.
	if col := s2.StartColumn(); col < 0 {
		t.Errorf("after SetPosition(5), before Scan: StartColumn() = %d, want >= 0", col)
	}
	if k := s2.Scan(); k != OpenBracket {
		t.Fatalf("token = %v, want OpenBracket", k)
	}
	if s2.StartLine() != 2 || s2.StartColumn() != 2 {
		t.Errorf("after SetPosition(5): line %d col %d, want 2,2", s2.StartLine(), s2.StartColumn())
	}

	// \r\n counts as one line break across a jump.
	s3 := NewScanner("{\r\n[]")
	s3.SetPosition(3)
	s3.Scan()
	if s3.StartLine() != 1 || s3.StartColumn() != 0 {
		t.Errorf("after SetPosition(3) over CRLF: line %d col %d, want 1,0", s3.StartLine(), s3.StartColumn())
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
	// Surrogate pair must decode to one rune, not two replacement chars.
	assertValue(t, `"😀"`, StringLiteral, "😀", ScanNone)
	// Multi-byte input passes through untouched.
	assertValue(t, `"héllo"`, StringLiteral, "héllo", ScanNone)
}

func TestScanStringErrors(t *testing.T) {
	assertValue(t, `"unterminated`, StringLiteral, "unterminated", ScanUnexpectedEndOfString)
	// A line break terminates a string: the value stops there.
	assertValue(t, "\"broken\nrest\"", StringLiteral, "broken", ScanUnexpectedEndOfString)
	assertValue(t, `"\q"`, StringLiteral, "�", ScanInvalidEscapeCharacter)
	assertValue(t, `"\u00"`, StringLiteral, "�", ScanInvalidUnicode)
	// A lone high surrogate yields U+FFFD and an error, not a panic.
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
	// Value is the RAW source text, not a normalized number.
	assertValue(t, "1.50", NumericLiteral, "1.50", ScanNone)
}

func TestScanNumberErrors(t *testing.T) {
	assertValue(t, "1e", NumericLiteral, "1e", ScanUnexpectedEndOfNumber)
	assertValue(t, "1.", NumericLiteral, "1.", ScanUnexpectedEndOfNumber)
	assertValue(t, "-", Unknown, "-", ScanInvalidCharacter)
}

func TestScanKeywords(t *testing.T) {
	assertValue(t, "true", TrueKeyword, "true", ScanNone)
	assertValue(t, "false", FalseKeyword, "false", ScanNone)
	assertValue(t, "null", NullKeyword, "null", ScanNone)
	// An unrecognized word is a single Unknown token covering the whole word.
	assertValue(t, "tru", Unknown, "tru", ScanInvalidCharacter)
	assertValue(t, "undefined", Unknown, "undefined", ScanInvalidCharacter)
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
	// A line comment stops at the line break; it does not consume it.
	assertKinds(t, "// c\n{", LineComment, LineBreak, OpenBrace)
	assertKinds(t, "/* a */{", BlockComment, OpenBrace)
	// A block comment spanning lines keeps line tracking correct.
	assertKinds(t, "/* a\nb */{", BlockComment, OpenBrace)
}

func TestScanCommentErrors(t *testing.T) {
	assertValue(t, "/* unterminated", BlockComment, "/* unterminated", ScanUnexpectedEndOfComment)
	// A lone slash is not a comment.
	assertValue(t, "/", Unknown, "/", ScanInvalidCharacter)
	assertValue(t, "/x", Unknown, "/", ScanInvalidCharacter)
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
	// Two line breaks were consumed: one inside the comment, one after it.
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
