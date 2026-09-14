package jsonc

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// SyntaxKind identifies a token produced by Scanner.
type SyntaxKind int

const (
	// Unknown is the zero value: a character the scanner could not classify.
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

// ScanError describes a problem with the token the scanner just produced.
// The scanner still produces a token and a value; it does not stop.
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

// Scanner tokenizes a JSONC document. It is stateful and not safe for
// concurrent use. All offsets are byte offsets into the text.
type Scanner struct {
	text string
	pos  int // byte offset of the next character to read

	token       SyntaxKind
	tokenOffset int
	tokenLength int
	value       string
	err         ScanError

	// Line tracking is incremental: these advance as line terminators are
	// consumed, so StartColumn is O(1) rather than a rescan.
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

// Token returns the kind of the most recently scanned token.
func (s *Scanner) Token() SyntaxKind { return s.token }

// Value returns the raw text of the most recently scanned token.
func (s *Scanner) Value() string { return s.value }

// Offset returns the byte offset where the most recently scanned token starts.
func (s *Scanner) Offset() int { return s.tokenOffset }

// Length returns the byte length of the most recently scanned token.
func (s *Scanner) Length() int { return s.tokenLength }

// Position returns the byte offset the scanner will read from next.
func (s *Scanner) Position() int { return s.pos }

// TokenError returns the scan error, if any, for the most recently scanned token.
func (s *Scanner) TokenError() ScanError { return s.err }

// StartLine returns the 0-based line number on which the most recently
// scanned token starts.
func (s *Scanner) StartLine() int { return s.tokenLineNumber }

// StartColumn is the 0-based byte offset of the token from the start of its line.
func (s *Scanner) StartColumn() int { return s.tokenOffset - s.tokenLineStartOffset }

// SetPosition moves the scanner to a byte offset and resets token state.
//
// Line and column tracking is recomputed for the new position by counting the
// line terminators before it, so StartLine and StartColumn stay absolute and
// correct after a jump. That costs one linear scan of the text up to pos;
// SetPosition is not meant for tight loops over a large document.
//
// node-jsonc-parser leaves its line counters untouched here, which lets a
// backward jump report a stale line and a NEGATIVE column. This port fixes
// that: a column is a byte count from the start of a line and can never be
// negative.
func (s *Scanner) SetPosition(pos int) {
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
		default:
			if c == '-' || isDigit(c) {
				if _, ok := s.scanNumber(); ok {
					s.token = NumericLiteral
					break
				}
				// A bare "-" with no digits is not a number. Advance
				// past it: a token of zero length would make every
				// `for k := s.Scan(); k != EOF` loop spin forever.
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
	// String values are decoded in scanString; every other token's value is
	// its raw source text, sliced without allocating.
	if s.token != StringLiteral {
		s.value = s.text[s.tokenOffset:s.pos]
	}
	return s.token
}

// scanLineBreaks consumes a run of line terminators, updating line tracking.
// \r\n counts as one line break.
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

func isWhitespace(c byte) bool { return c == ' ' || c == '\t' || c == '\v' || c == '\f' }
func isLineBreak(c byte) bool  { return c == '\n' || c == '\r' }

// runeLenAt returns the byte length of the rune starting at i, always >= 1 so
// scanning cannot stall on invalid UTF-8.
func runeLenAt(s string, i int) int {
	_, n := utf8.DecodeRuneInString(s[i:])
	if n == 0 {
		return 1
	}
	return n
}

// scanString consumes a quoted string starting at the opening quote and
// returns its decoded value. It sets s.err on malformed input but always
// returns a usable value, matching upstream's behavior of reporting an error
// alongside a result rather than bailing.
func (s *Scanner) scanString() string {
	s.pos++ // opening quote
	start := s.pos
	var b *strings.Builder // allocated only when an escape is found

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
			// An unescaped line break terminates the string.
			s.err = ScanUnexpectedEndOfString
			return s.flush(b, start)
		case c == '\\':
			if b == nil {
				b = &strings.Builder{}
				b.WriteString(s.text[start:s.pos])
			}
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

// flush returns the builder's contents, or a zero-copy slice of the input when
// no escape was ever encountered.
func (s *Scanner) flush(b *strings.Builder, start int) string {
	if b == nil {
		return s.text[start:s.pos]
	}
	b.WriteString(s.text[start:s.pos])
	return b.String()
}

// writeEscape consumes one escape sequence (s.pos is just past the backslash)
// and writes its value to b.
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
			// Try to pair it with a following \uXXXX.
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

// scanHex4 reads up to four hex digits, advancing s.pos past however many
// valid hex digits it finds and reporting ok=false if fewer than four were
// found. s.pos always stops at the first non-hex character (or end of text)
// rather than jumping past it, so a truncated escape like "\u00" (followed
// immediately by the closing quote) leaves the scanner positioned AT that
// quote instead of overshooting past it — otherwise the enclosing string
// scan would run off the end of the text and report
// ScanUnexpectedEndOfString instead of ScanInvalidUnicode.
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

// scanNumber consumes a JSON number and returns its raw source text. It
// returns ok=false when there is no number at all (a bare "-"). A truncated
// number such as "1e" returns the text with s.err set, matching upstream.
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
