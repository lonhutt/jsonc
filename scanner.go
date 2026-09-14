package jsonc

import "unicode/utf8"

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
		default:
			// Strings, numbers, keywords, and comments land here until
			// Tasks 2 and 3. Advance by one rune so an unknown multi-byte
			// character cannot split and produce invalid UTF-8 slices.
			s.pos += runeLenAt(s.text, s.pos)
			s.token = Unknown
		}
	}

	s.tokenLength = s.pos - s.tokenOffset
	s.value = s.text[s.tokenOffset:s.pos]
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
