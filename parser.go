package jsonc

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// ParseErrorCode identifies a recoverable problem found while parsing.
// Codes start at 1; the zero value is never a valid code.
type ParseErrorCode int

const (
	InvalidSymbol ParseErrorCode = iota + 1
	InvalidNumberFormat
	PropertyNameExpected
	ValueExpected
	ColonExpected
	CommaExpected
	CloseBraceExpected
	CloseBracketExpected
	EndOfFileExpected
	InvalidCommentToken
	UnexpectedEndOfComment
	UnexpectedEndOfString
	UnexpectedEndOfNumber
	InvalidUnicode
	InvalidEscapeCharacter
	InvalidCharacter
)

var parseErrorCodeNames = [...]string{
	"InvalidSymbol", "InvalidNumberFormat", "PropertyNameExpected",
	"ValueExpected", "ColonExpected", "CommaExpected", "CloseBraceExpected",
	"CloseBracketExpected", "EndOfFileExpected", "InvalidCommentToken",
	"UnexpectedEndOfComment", "UnexpectedEndOfString", "UnexpectedEndOfNumber",
	"InvalidUnicode", "InvalidEscapeCharacter", "InvalidCharacter",
}

// Adding a code to the const block above without adding its name here is a
// compile error: this index is out of range unless the table covers the last
// code. Without it, String would silently return "ParseErrorCode(17)".
var _ = parseErrorCodeNames[InvalidCharacter-1]

// String returns the code's name, replacing upstream's printParseErrorCode.
func (c ParseErrorCode) String() string {
	i := int(c) - 1
	if i < 0 || i >= len(parseErrorCodeNames) {
		return "ParseErrorCode(" + strconv.Itoa(int(c)) + ")"
	}
	return parseErrorCodeNames[i]
}

// ParseError is a single recoverable parse problem. Parsing continues after
// one is reported, so callers receive a slice of them alongside a best-effort
// result. All offsets are byte offsets.
type ParseError struct {
	Code           ParseErrorCode
	Offset, Length int
	StartLine      int // 0-based
	StartCol       int // 0-based, bytes from the start of the line
}

func (e ParseError) Error() string {
	return fmt.Sprintf("%s at offset %d (line %d, column %d)", e.Code, e.Offset, e.StartLine, e.StartCol)
}

type parseConfig struct {
	disallowComments   bool
	allowTrailingComma bool
	allowEmptyContent  bool
	useNumber          bool
}

// ParseOption configures parsing. Construct one with the functions below.
type ParseOption func(*parseConfig)

// DisallowComments reports a comment as InvalidCommentToken instead of
// accepting it.
func DisallowComments() ParseOption {
	return func(c *parseConfig) { c.disallowComments = true }
}

// AllowTrailingComma accepts a comma before a closing brace or bracket.
func AllowTrailingComma() ParseOption {
	return func(c *parseConfig) { c.allowTrailingComma = true }
}

// AllowEmptyContent accepts an empty document instead of reporting
// ValueExpected.
func AllowEmptyContent() ParseOption {
	return func(c *parseConfig) { c.allowEmptyContent = true }
}

// UseNumber decodes numeric literals as json.Number rather than float64,
// preserving exact large integers and decimals.
func UseNumber() ParseOption {
	return func(c *parseConfig) { c.useNumber = true }
}

func newParseConfig(opts []ParseOption) parseConfig {
	var c parseConfig
	for _, o := range opts {
		o(&c)
	}
	return c
}

// Span locates a visitor event in the source text. All values are byte based.
type Span struct {
	Offset, Length int
	StartLine      int // 0-based
	StartCol       int // 0-based, bytes from the start of the line
	// Path returns the JSON path of the event's position. It is never nil.
	// The path is snapshotted when the Span is created, not read from the live
	// parser stack when called, so it stays correct however long a caller holds
	// on to the Span.
	//
	// Every call returns the same slice, so the caller must not modify it;
	// copy it first if you need to retain a mutable path.
	Path func() Path
}

// Visitor holds the callbacks Visit invokes. Every field is optional; set only
// the ones you need.
type Visitor struct {
	OnObjectBegin    func(Span)
	OnObjectEnd      func(Span)
	OnArrayBegin     func(Span)
	OnArrayEnd       func(Span)
	OnObjectProperty func(name string, s Span)
	OnLiteralValue   func(v any, s Span)
	OnSeparator      func(sep string, s Span)
	OnComment        func(Span)
	OnError          func(code ParseErrorCode, s Span)
}

type visitState struct {
	s   *Scanner
	v   Visitor
	cfg parseConfig
	// path is the live JSON path stack. Object property names and array
	// indices are written in place as parsing advances.
	path Path
}

// Visit parses text and invokes v's callbacks for each object, array, literal,
// separator, and comment. It reports every recoverable error through
// v.OnError and keeps going.
//
// It returns false only when parsing could not produce a value at all; parse
// errors alone do not make it return false.
func Visit(text string, v Visitor, opts ...ParseOption) bool {
	st := &visitState{
		s:   NewScanner(text),
		v:   v,
		cfg: newParseConfig(opts),
	}

	st.scanNext()
	if st.s.Token() == EOF {
		if st.cfg.allowEmptyContent {
			return true
		}
		st.handleError(ValueExpected, nil, nil)
		return false
	}
	if !st.parseValue() {
		st.handleError(ValueExpected, nil, nil)
		return false
	}
	if st.s.Token() != EOF {
		st.handleError(EndOfFileExpected, nil, nil)
	}
	return true
}

// span builds a Span for the scanner's current token.
func (st *visitState) span() Span {
	// Copy the path stack now; the closure must not observe later mutations.
	snapshot := make(Path, len(st.path))
	copy(snapshot, st.path)
	return Span{
		Offset:    st.s.Offset(),
		Length:    st.s.Length(),
		StartLine: st.s.StartLine(),
		StartCol:  st.s.StartColumn(),
		Path:      func() Path { return snapshot },
	}
}

// scanNext advances past trivia, translating scan errors into parse errors and
// dispatching comments, and returns the next significant token.
func (st *visitState) scanNext() SyntaxKind {
	for {
		token := st.s.Scan()

		switch st.s.TokenError() {
		case ScanInvalidUnicode:
			st.handleError(InvalidUnicode, nil, nil)
		case ScanInvalidEscapeCharacter:
			st.handleError(InvalidEscapeCharacter, nil, nil)
		case ScanUnexpectedEndOfNumber:
			st.handleError(UnexpectedEndOfNumber, nil, nil)
		case ScanUnexpectedEndOfComment:
			st.handleError(UnexpectedEndOfComment, nil, nil)
		case ScanUnexpectedEndOfString:
			st.handleError(UnexpectedEndOfString, nil, nil)
		case ScanInvalidCharacter:
			st.handleError(InvalidCharacter, nil, nil)
		}

		switch token {
		case LineComment, BlockComment:
			if st.cfg.disallowComments {
				st.handleError(InvalidCommentToken, nil, nil)
			} else if st.v.OnComment != nil {
				st.v.OnComment(st.span())
			}
		case Unknown:
			st.handleError(InvalidSymbol, nil, nil)
		case Trivia, LineBreak:
			// skip
		default:
			return token
		}
	}
}

// handleError reports code, then skips tokens to resynchronize. It stops after
// a token in skipUntilAfter (consuming it), or before a token in skipUntil.
//
// skipUntilAfter exists for parity with upstream's visit. Both call sites that
// pass it (the CloseBraceExpected and CloseBracketExpected reports) sit after
// their container loop has already run to EOF, so in practice the skip loop
// never runs and the parameter never actually consumes a token.
func (st *visitState) handleError(code ParseErrorCode, skipUntilAfter, skipUntil []SyntaxKind) {
	if st.v.OnError != nil {
		st.v.OnError(code, st.span())
	}
	if len(skipUntilAfter) == 0 && len(skipUntil) == 0 {
		return
	}
	token := st.s.Token()
	for token != EOF {
		if slices.Contains(skipUntilAfter, token) {
			st.scanNext()
			return
		}
		if slices.Contains(skipUntil, token) {
			return
		}
		token = st.scanNext()
	}
}

func (st *visitState) parseValue() bool {
	switch st.s.Token() {
	case OpenBracket:
		return st.parseArray()
	case OpenBrace:
		return st.parseObject()
	case StringLiteral:
		// Identical to default on purpose: upstream calls parseString(true)
		// here, and keeping the arm keeps this function diffable against it.
		return st.parseLiteral()
	default:
		return st.parseLiteral()
	}
}

func (st *visitState) parseLiteral() bool {
	switch st.s.Token() {
	case NumericLiteral:
		raw := st.s.Value()
		var value any
		if st.cfg.useNumber {
			value = json.Number(raw)
			// Only a SYNTAX error invalidates a json.Number. A value out of
			// float64's range is still a perfectly good sequence of digits,
			// and preserving those digits is the entire reason UseNumber
			// exists — clobbering 1e400 to "0" would defeat it. This matches
			// encoding/json, whose UseNumber decoder accepts 1e400 verbatim.
			if _, err := strconv.ParseFloat(raw, 64); err != nil {
				var ne *strconv.NumError
				if !errors.As(err, &ne) || ne.Err != strconv.ErrRange {
					st.handleError(InvalidNumberFormat, nil, nil)
					value = json.Number("0")
				}
			}
		} else {
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				st.handleError(InvalidNumberFormat, nil, nil)
				f = 0
			}
			value = f
		}
		if st.v.OnLiteralValue != nil {
			st.v.OnLiteralValue(value, st.span())
		}
	case StringLiteral:
		if st.v.OnLiteralValue != nil {
			st.v.OnLiteralValue(st.s.Value(), st.span())
		}
	case NullKeyword:
		if st.v.OnLiteralValue != nil {
			st.v.OnLiteralValue(nil, st.span())
		}
	case TrueKeyword:
		if st.v.OnLiteralValue != nil {
			st.v.OnLiteralValue(true, st.span())
		}
	case FalseKeyword:
		if st.v.OnLiteralValue != nil {
			st.v.OnLiteralValue(false, st.span())
		}
	default:
		return false
	}
	st.scanNext()
	return true
}

func (st *visitState) separator(sep string) {
	if st.v.OnSeparator != nil {
		st.v.OnSeparator(sep, st.span())
	}
}

func (st *visitState) parseProperty() bool {
	if st.s.Token() != StringLiteral {
		st.handleError(PropertyNameExpected, nil, []SyntaxKind{CloseBrace, Comma})
		return false
	}
	name := st.s.Value()
	// Write the name into the live path before descending into the value.
	st.path[len(st.path)-1] = name
	if st.v.OnObjectProperty != nil {
		st.v.OnObjectProperty(name, st.span())
	}
	st.scanNext()

	if st.s.Token() != Colon {
		st.handleError(ColonExpected, nil, []SyntaxKind{CloseBrace, Comma})
		return true
	}
	st.separator(":")
	st.scanNext()
	if !st.parseValue() {
		st.handleError(ValueExpected, nil, []SyntaxKind{CloseBrace, Comma})
	}
	return true
}

func (st *visitState) parseObject() bool {
	if st.v.OnObjectBegin != nil {
		st.v.OnObjectBegin(st.span())
	}
	// Push a placeholder; parseProperty overwrites it with each key.
	st.path = append(st.path, nil)
	st.scanNext()

	needsComma := false
	for st.s.Token() != CloseBrace && st.s.Token() != EOF {
		if st.s.Token() == Comma {
			if !needsComma {
				st.handleError(ValueExpected, nil, nil)
			}
			st.separator(",")
			st.scanNext()
			if st.s.Token() == CloseBrace && st.cfg.allowTrailingComma {
				break
			}
		} else if needsComma {
			st.handleError(CommaExpected, nil, nil)
		}
		if !st.parseProperty() {
			st.handleError(ValueExpected, nil, []SyntaxKind{CloseBrace, Comma})
		}
		needsComma = true
	}

	st.path = st.path[:len(st.path)-1]
	if st.v.OnObjectEnd != nil {
		st.v.OnObjectEnd(st.span())
	}
	if st.s.Token() != CloseBrace {
		st.handleError(CloseBraceExpected, []SyntaxKind{CloseBrace}, nil)
	} else {
		st.scanNext()
	}
	return true
}

func (st *visitState) parseArray() bool {
	if st.v.OnArrayBegin != nil {
		st.v.OnArrayBegin(st.span())
	}
	st.path = append(st.path, 0)
	st.scanNext()

	needsComma := false
	for st.s.Token() != CloseBracket && st.s.Token() != EOF {
		if st.s.Token() == Comma {
			if !needsComma {
				st.handleError(ValueExpected, nil, nil)
			}
			st.separator(",")
			st.scanNext()
			if st.s.Token() == CloseBracket && st.cfg.allowTrailingComma {
				break
			}
		} else if needsComma {
			st.handleError(CommaExpected, nil, nil)
		}
		if !st.parseValue() {
			st.handleError(ValueExpected, nil, []SyntaxKind{CloseBracket, Comma})
		}
		// Advance the array index in the live path.
		if i, ok := st.path[len(st.path)-1].(int); ok {
			st.path[len(st.path)-1] = i + 1
		}
		needsComma = true
	}

	st.path = st.path[:len(st.path)-1]
	if st.v.OnArrayEnd != nil {
		st.v.OnArrayEnd(st.span())
	}
	if st.s.Token() != CloseBracket {
		st.handleError(CloseBracketExpected, []SyntaxKind{CloseBracket}, nil)
	} else {
		st.scanNext()
	}
	return true
}
