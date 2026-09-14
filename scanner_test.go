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
