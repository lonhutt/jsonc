package jsonc

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// SyntaxKind identifies a token produced by Scanner.
type SyntaxKind int

const (
	// Unknown is a character the scanner couldn't classify.
	Unknown SyntaxKind = iota
	OpenBrace
	CloseBrace
	OpenBracket
	CloseBracket
	Comma
	Colon
	NullKeyword
	TrueKeyword
	FalseKeyword
	StringLiteral
	NumericLiteral
	LineComment
	BlockComment
	LineBreak
	Trivia
	EOF
)

var syntaxKindNames = [...]string{
	"Unknown", "OpenBrace", "CloseBrace", "OpenBracket", "CloseBracket",
	"Comma", "Colon", "NullKeyword", "TrueKeyword", "FalseKeyword",
	"StringLiteral", "NumericLiteral", "LineComment", "BlockComment",
	"LineBreak", "Trivia", "EOF",
}

func (k SyntaxKind) String() string {
	if k < 0 || int(k) >= len(syntaxKindNames) {
		return "SyntaxKind(?)"
	}
	return syntaxKindNames[k]
}

// ScanError is a problem with the last token; the token and its value are
// still usable.
type ScanError int

const (
	ScanNone ScanError = iota
	ScanUnexpectedEndOfComment
	ScanUnexpectedEndOfString
	ScanUnexpectedEndOfNumber
	ScanInvalidUnicode
	ScanInvalidEscapeCharacter
	ScanInvalidCharacter
)

var scanErrorNames = [...]string{
	"None", "UnexpectedEndOfComment", "UnexpectedEndOfString",
	"UnexpectedEndOfNumber", "InvalidUnicode", "InvalidEscapeCharacter",
	"InvalidCharacter",
}

func (e ScanError) String() string {
	if e < 0 || int(e) >= len(scanErrorNames) {
		return "ScanError(?)"
	}
	return scanErrorNames[e]
}

type scannerConfig struct {
	ignoreTrivia bool
}

// ScannerOption configures a Scanner. Construct one with IgnoreTrivia.
type ScannerOption func(*scannerConfig)

// IgnoreTrivia makes Scan skip whitespace, line breaks, and comments.
func IgnoreTrivia() ScannerOption {
	return func(c *scannerConfig) { c.ignoreTrivia = true }
}

// Scanner tokenizes a JSONC document. Offsets are bytes. Not safe for
// concurrent use.
type Scanner struct {
	text string
	pos  int // next byte to read

	token       SyntaxKind
	tokenOffset int
	tokenLength int
	value       string
	err         ScanError

	// Advanced as terminators are consumed, so StartColumn is O(1).
	lineNumber      int
	lineStartOffset int

	tokenLineNumber      int
	tokenLineStartOffset int

	ignoreTrivia bool
}

// NewScanner returns a Scanner positioned before the first token of text.
func NewScanner(text string, opts ...ScannerOption) *Scanner {
	var c scannerConfig
	for _, o := range opts {
		o(&c)
	}
	return &Scanner{text: text, ignoreTrivia: c.ignoreTrivia}
}

// Token returns the kind of the last scanned token.
func (s *Scanner) Token() SyntaxKind { return s.token }

// Value returns the last token's text; decoded for a string, raw otherwise.
func (s *Scanner) Value() string { return s.value }

// Offset returns where the last token starts.
func (s *Scanner) Offset() int { return s.tokenOffset }

// Length returns the last token's length in bytes.
func (s *Scanner) Length() int { return s.tokenLength }

// Position returns the offset the scanner reads from next.
func (s *Scanner) Position() int { return s.pos }

// TokenError returns the last token's scan error, if any.
func (s *Scanner) TokenError() ScanError { return s.err }

// StartLine returns the 0-based line the last token starts on.
func (s *Scanner) StartLine() int { return s.tokenLineNumber }

// StartColumn returns the last token's 0-based byte offset within its line.
func (s *Scanner) StartColumn() int { return s.tokenOffset - s.tokenLineStartOffset }

// SetPosition moves the scanner to pos (negative clamps to 0) and resets
// token state. Line tracking is recounted from the start of the text, so it's
// linear in pos; not meant for tight loops.
//
// Upstream leaves its line counters alone here, so a backward jump can report
// a stale line and a negative column; this doesn't.
func (s *Scanner) SetPosition(pos int) {
	pos = max(pos, 0)
	s.pos = pos
	s.value = ""
	s.tokenOffset = pos
	s.tokenLength = 0
	s.token = Unknown
	s.err = ScanNone

	s.lineNumber = 0
	s.lineStartOffset = 0
	for i := 0; i < pos && i < len(s.text); i++ {
		if s.text[i] == '\r' {
			if i+1 < pos && i+1 < len(s.text) && s.text[i+1] == '\n' {
				i++
			}
			s.lineNumber++
			s.lineStartOffset = i + 1
		} else if s.text[i] == '\n' {
			s.lineNumber++
			s.lineStartOffset = i + 1
		}
	}
	s.tokenLineNumber = s.lineNumber
	s.tokenLineStartOffset = s.lineStartOffset
}

// Scan advances to the next token and returns its kind.
func (s *Scanner) Scan() SyntaxKind {
	if s.ignoreTrivia {
		for {
			k := s.scanNext()
			switch k {
			case Trivia, LineBreak, LineComment, BlockComment:
				continue
			default:
				return k
			}
		}
	}
	return s.scanNext()
}

func (s *Scanner) scanNext() SyntaxKind {
	s.value = ""
	s.err = ScanNone
	s.tokenOffset = s.pos
	s.tokenLineNumber = s.lineNumber
	s.tokenLineStartOffset = s.lineStartOffset

	if s.pos >= len(s.text) {
		s.tokenLength = 0
		s.token = EOF
		return EOF
	}

	c := s.text[s.pos]

	switch {
	case isLineBreak(c):
		s.scanLineBreaks()
		s.token = LineBreak
	case isWhitespace(c):
		for s.pos < len(s.text) && isWhitespace(s.text[s.pos]) {
			s.pos++
		}
		s.token = Trivia
	default:
		switch c {
		case '{':
			s.pos++
			s.token = OpenBrace
		case '}':
			s.pos++
			s.token = CloseBrace
		case '[':
			s.pos++
			s.token = OpenBracket
		case ']':
			s.pos++
			s.token = CloseBracket
		case ':':
			s.pos++
			s.token = Colon
		case ',':
			s.pos++
			s.token = Comma
		case '"':
			s.token = StringLiteral
			s.value = s.scanString()
		case '/':
			if s.pos+1 < len(s.text) && s.text[s.pos+1] == '/' {
				s.pos += 2
				for s.pos < len(s.text) && !isLineBreak(s.text[s.pos]) {
					s.pos++
				}
				s.token = LineComment
				break
			}
			if s.pos+1 < len(s.text) && s.text[s.pos+1] == '*' {
				s.pos += 2
				s.scanBlockCommentBody()
				s.token = BlockComment
				break
			}
			s.pos++
			s.token = Unknown
			s.err = ScanInvalidCharacter
		default:
			if c == '-' || isDigit(c) {
				if _, ok := s.scanNumber(); ok {
					s.token = NumericLiteral
					break
				}
				// A bare "-" still has to advance; a zero-length token
				// spins every Scan loop forever.
				s.pos += runeLenAt(s.text, s.pos)
				s.token = Unknown
				s.err = ScanInvalidCharacter
				break
			}
			if isLetter(c) {
				start := s.pos
				for s.pos < len(s.text) && isLetter(s.text[s.pos]) {
					s.pos++
				}
				switch s.text[start:s.pos] {
				case "true":
					s.token = TrueKeyword
				case "false":
					s.token = FalseKeyword
				case "null":
					s.token = NullKeyword
				default:
					s.token = Unknown
					s.err = ScanInvalidCharacter
				}
				break
			}
			s.pos += runeLenAt(s.text, s.pos)
			s.token = Unknown
			s.err = ScanInvalidCharacter
		}
	}

	s.tokenLength = s.pos - s.tokenOffset
	// scanString already set the decoded value.
	if s.token != StringLiteral {
		s.value = s.text[s.tokenOffset:s.pos]
	}
	return s.token
}

// scanLineBreaks consumes a run of terminators; \r\n counts as one.
func (s *Scanner) scanLineBreaks() {
	for s.pos < len(s.text) && isLineBreak(s.text[s.pos]) {
		if s.text[s.pos] == '\r' && s.pos+1 < len(s.text) && s.text[s.pos+1] == '\n' {
			s.pos++
		}
		s.pos++
		s.lineNumber++
		s.lineStartOffset = s.pos
	}
}

// scanBlockCommentBody consumes everything after the opening "/*".
func (s *Scanner) scanBlockCommentBody() {
	for s.pos < len(s.text) {
		c := s.text[s.pos]
		if c == '*' && s.pos+1 < len(s.text) && s.text[s.pos+1] == '/' {
			s.pos += 2
			return
		}
		if isLineBreak(c) {
			s.scanLineBreaks()
			continue
		}
		s.pos += runeLenAt(s.text, s.pos)
	}
	s.err = ScanUnexpectedEndOfComment
}

func isWhitespace(c byte) bool { return c == ' ' || c == '\t' || c == '\v' || c == '\f' }
func isLineBreak(c byte) bool  { return c == '\n' || c == '\r' }

// runeLenAt is never 0, so invalid UTF-8 can't stall the scanner.
func runeLenAt(s string, i int) int {
	_, n := utf8.DecodeRuneInString(s[i:])
	if n == 0 {
		return 1
	}
	return n
}

// scanString returns the decoded string at s.pos. Malformed input sets s.err
// but still returns a value, as upstream does.
func (s *Scanner) scanString() string {
	s.pos++ // opening quote
	start := s.pos
	var b *strings.Builder // only allocated once there's an escape

	for {
		if s.pos >= len(s.text) {
			s.err = ScanUnexpectedEndOfString
			return s.flush(b, start)
		}
		c := s.text[s.pos]
		switch {
		case c == '"':
			out := s.flush(b, start)
			s.pos++ // closing quote
			return out
		case isLineBreak(c):
			// an unescaped line break ends the string
			s.err = ScanUnexpectedEndOfString
			return s.flush(b, start)
		case c == '\\':
			if b == nil {
				b = &strings.Builder{}
			}
			// Flush on every escape, not just the first; otherwise the text
			// between two escapes is dropped.
			b.WriteString(s.text[start:s.pos])
			s.pos++
			if s.pos >= len(s.text) {
				s.err = ScanUnexpectedEndOfString
				return b.String()
			}
			s.writeEscape(b)
			start = s.pos
		default:
			s.pos++
		}
	}
}

// flush returns the decoded string, or a slice of the input if there were no
// escapes.
func (s *Scanner) flush(b *strings.Builder, start int) string {
	if b == nil {
		return s.text[start:s.pos]
	}
	b.WriteString(s.text[start:s.pos])
	return b.String()
}

// writeEscape decodes the escape after a backslash into b.
func (s *Scanner) writeEscape(b *strings.Builder) {
	c := s.text[s.pos]
	s.pos++
	switch c {
	case '"':
		b.WriteByte('"')
	case '\\':
		b.WriteByte('\\')
	case '/':
		b.WriteByte('/')
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'u':
		r, ok := s.scanHex4()
		if !ok {
			s.err = ScanInvalidUnicode
			b.WriteRune(utf8.RuneError)
			return
		}
		if utf16.IsSurrogate(rune(r)) {
			// pair it with a following \uXXXX, if there is one
			if s.pos+1 < len(s.text) && s.text[s.pos] == '\\' && s.text[s.pos+1] == 'u' {
				save := s.pos
				s.pos += 2
				if r2, ok2 := s.scanHex4(); ok2 {
					if combined := utf16.DecodeRune(rune(r), rune(r2)); combined != utf8.RuneError {
						b.WriteRune(combined)
						return
					}
				}
				s.pos = save
			}
			s.err = ScanInvalidUnicode
			b.WriteRune(utf8.RuneError)
			return
		}
		b.WriteRune(rune(r))
	default:
		s.err = ScanInvalidEscapeCharacter
		b.WriteRune(utf8.RuneError)
	}
}

// scanHex4 reads up to four hex digits and stops on the first non-hex byte.
// Stopping there matters; "\u00" right before the closing quote has to leave
// s.pos on the quote, or the string runs off the end and reports the wrong
// error.
func (s *Scanner) scanHex4() (int, bool) {
	v := 0
	digits := 0
	for digits < 4 && s.pos < len(s.text) {
		d := hexVal(s.text[s.pos])
		if d < 0 {
			break
		}
		v = v*16 + d
		s.pos++
		digits++
	}
	if digits < 4 {
		return 0, false
	}
	return v, true
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// scanNumber returns the number at s.pos, or ok=false for a bare "-". A
// truncated number like "1e" comes back with s.err set, as upstream does.
func (s *Scanner) scanNumber() (string, bool) {
	start := s.pos
	if s.pos < len(s.text) && s.text[s.pos] == '-' {
		s.pos++
	}
	if s.pos >= len(s.text) || !isDigit(s.text[s.pos]) {
		s.pos = start
		return "", false
	}
	for s.pos < len(s.text) && isDigit(s.text[s.pos]) {
		s.pos++
	}
	if s.pos < len(s.text) && s.text[s.pos] == '.' {
		s.pos++
		if s.pos >= len(s.text) || !isDigit(s.text[s.pos]) {
			s.err = ScanUnexpectedEndOfNumber
			return s.text[start:s.pos], true
		}
		for s.pos < len(s.text) && isDigit(s.text[s.pos]) {
			s.pos++
		}
	}
	if s.pos < len(s.text) && (s.text[s.pos] == 'e' || s.text[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.text) && (s.text[s.pos] == '+' || s.text[s.pos] == '-') {
			s.pos++
		}
		if s.pos >= len(s.text) || !isDigit(s.text[s.pos]) {
			s.err = ScanUnexpectedEndOfNumber
			return s.text[start:s.pos], true
		}
		for s.pos < len(s.text) && isDigit(s.text[s.pos]) {
			s.pos++
		}
	}
	return s.text[start:s.pos], true
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
