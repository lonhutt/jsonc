package jsonc

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// ParseErrorCode identifies a recoverable parse problem. Codes start at 1.
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

// Fails to compile if a code is added without a name.
var _ = parseErrorCodeNames[InvalidCharacter-1]

// String returns the code's name; upstream's printParseErrorCode.
func (c ParseErrorCode) String() string {
	i := int(c) - 1
	if i < 0 || i >= len(parseErrorCodeNames) {
		return "ParseErrorCode(" + strconv.Itoa(int(c)) + ")"
	}
	return parseErrorCodeNames[i]
}

// ParseError is one recoverable parse problem. Offsets are bytes.
type ParseError struct {
	Code           ParseErrorCode
	Offset, Length int
	StartLine      int // 0-based
	StartCol       int // 0-based, in bytes
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

// ParseOption configures parsing.
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

// UseNumber decodes numbers as json.Number instead of float64, so large
// integers and decimals stay exact.
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

// Span locates a visitor event in the text. Offsets are bytes.
type Span struct {
	Offset, Length int
	StartLine      int // 0-based
	StartCol       int // 0-based, in bytes
	// Path returns the JSON path at the event; never nil. It's a snapshot, so
	// it stays right however long the Span is held. Every call returns the same
	// slice; copy it before modifying.
	Path func() Path
}

// Visitor holds Visit's callbacks; all optional.
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
	// path is live; keys and indices are overwritten in place as parsing moves.
	path Path
}

// Visit parses text, calling v's callbacks as it goes. Errors go to v.OnError
// and parsing continues; it returns false only when there's no value at all.
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

func (st *visitState) span() Span {
	// copy now; the closure mustn't see later changes to the live path
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

// scanNext returns the next significant token, reporting scan errors and
// comments it passes.
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
		default:
			return token
		}
	}
}

// handleError reports code, then skips until it has consumed a token in
// skipUntilAfter or reached one in skipUntil.
//
// skipUntilAfter is only for parity with upstream; both callers are already at
// EOF, so it never consumes anything.
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
		// same as default; kept so this diffs cleanly against upstream's
		// parseString(true)
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
			// Only a syntax error invalidates a json.Number. 1e400 overflows
			// float64 but the digits are still exact, which is the point of
			// UseNumber; encoding/json keeps it too.
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
	// before the value, so its events see this key
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
	// placeholder; parseProperty fills in each key
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

// Parse returns the value of text as json.Unmarshal into an any would:
// map[string]any, []any, string, bool, float64 (json.Number with UseNumber),
// or nil.
//
// It's fault tolerant; a best-effort value comes back with every problem
// found, so check len(errs) == 0 to be strict. Key order isn't kept; use
// ParseTree for that.
func Parse(text string, opts ...ParseOption) (any, []ParseError) {
	var (
		errs    []ParseError
		result  any
		stack   []any // open containers: *[]any or map[string]any
		keys    []string
		hasRoot bool
	)

	// set stores v in the open container, or as the result at the top level.
	set := func(v any) {
		if len(stack) == 0 {
			result = v
			hasRoot = true
			return
		}
		switch top := stack[len(stack)-1].(type) {
		case *[]any:
			*top = append(*top, v)
		case map[string]any:
			top[keys[len(keys)-1]] = v
		}
	}

	push := func(container any) {
		stack = append(stack, container)
	}

	pop := func() {
		done := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if arr, ok := done.(*[]any); ok {
			set(*arr)
			return
		}
		set(done)
	}

	Visit(text, Visitor{
		OnObjectBegin: func(Span) {
			push(map[string]any{})
			keys = append(keys, "")
		},
		OnObjectProperty: func(name string, _ Span) {
			keys[len(keys)-1] = name
		},
		OnObjectEnd: func(Span) {
			keys = keys[:len(keys)-1]
			pop()
		},
		OnArrayBegin: func(Span) {
			arr := make([]any, 0)
			push(&arr)
		},
		OnArrayEnd: func(Span) {
			pop()
		},
		OnLiteralValue: func(v any, _ Span) {
			set(v)
		},
		OnError: func(c ParseErrorCode, s Span) {
			errs = append(errs, ParseError{
				Code:      c,
				Offset:    s.Offset,
				Length:    s.Length,
				StartLine: s.StartLine,
				StartCol:  s.StartCol,
			})
		},
	}, opts...)

	if !hasRoot {
		return nil, errs
	}
	return result, errs
}
