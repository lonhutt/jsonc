package jsonc

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
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
	parseErrorCodeEnd // one past the last; not a code
)

var parseErrorCodeNames = [...]string{
	"InvalidSymbol", "InvalidNumberFormat", "PropertyNameExpected",
	"ValueExpected", "ColonExpected", "CommaExpected", "CloseBraceExpected",
	"CloseBracketExpected", "EndOfFileExpected", "InvalidCommentToken",
	"UnexpectedEndOfComment", "UnexpectedEndOfString", "UnexpectedEndOfNumber",
	"InvalidUnicode", "InvalidEscapeCharacter", "InvalidCharacter",
}

// Fails to compile unless every code has exactly one name: the index is 0 only
// when the counts match, and anything else is out of range for [1].
var _ = [1]struct{}{}[len(parseErrorCodeNames)-int(parseErrorCodeEnd-1)]

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
	path           *pathNode
}

// Path returns the JSON path at the event, as of the event however long the
// Span is held. Never nil; each call returns a new slice.
func (s Span) Path() Path {
	if s.path == nil {
		return Path{}
	}
	out := make(Path, s.path.depth)
	for n := s.path; n != nil; n = n.parent {
		out[n.depth-1] = n.seg
	}
	return out
}

// pathNode is one segment of a path. Nodes are never changed once made, so a
// Span keeps a pointer instead of a copy of the path.
type pathNode struct {
	parent *pathNode
	seg    any
	depth  int
}

func (n *pathNode) push(seg any) *pathNode {
	depth := 1
	if n != nil {
		depth = n.depth + 1
	}
	return &pathNode{parent: n, seg: seg, depth: depth}
}

// Visitor holds Visit's callbacks; all optional. A Begin callback that
// returns false skips the container's contents: no callbacks, errors
// included, until its End.
type Visitor struct {
	OnObjectBegin    func(Span) bool
	OnObjectEnd      func(Span)
	OnArrayBegin     func(Span) bool
	OnArrayEnd       func(Span)
	OnObjectProperty func(name string, s Span)
	OnLiteralValue   func(v any, s Span)
	OnSeparator      func(sep string, s Span)
	OnComment        func(Span)
	OnError          func(code ParseErrorCode, s Span)
}

type visitState struct {
	s        *Scanner
	v        Visitor
	cfg      parseConfig
	path     *pathNode // where parsing is; nil at the top
	withPath bool
	open     []container
	// skipped counts the open containers inside one a Begin callback skipped.
	skipped int
}

// begin fires a Begin callback. Depth is counted whether or not one is set;
// upstream only counts it when one is, so a nested container without one ends
// the skip early.
func (st *visitState) begin(cb func(Span) bool) {
	switch {
	case st.skipped > 0:
		st.skipped++
	case cb != nil && !cb(st.span()):
		st.skipped = 1
	}
}

// end fires an End callback; a skipped container's own End still fires.
func (st *visitState) end(cb func(Span)) {
	if st.skipped > 0 {
		st.skipped--
	}
	if st.skipped == 0 && cb != nil {
		cb(st.span())
	}
}

// Visit parses text, calling v's callbacks as it goes. Errors go to v.OnError
// and parsing continues; it returns false only when there's no value at all.
func Visit(text string, v Visitor, opts ...ParseOption) bool {
	return visit(text, v, true, opts...)
}

// visit is Visit; Parse, ParseTree and GetLocation pass withPath false, since
// they never read Span.Path and building it costs an allocation per member.
func visit(text string, v Visitor, withPath bool, opts ...ParseOption) bool {
	st := &visitState{
		s:        NewScanner(text),
		v:        v,
		cfg:      newParseConfig(opts),
		withPath: withPath,
	}

	st.scanNext()
	if st.s.Token() == EOF {
		if st.cfg.allowEmptyContent {
			return true
		}
		st.handleError(ValueExpected)
		return false
	}
	if !st.parseValue() {
		st.handleError(ValueExpected)
		return false
	}
	if st.s.Token() != EOF {
		st.handleError(EndOfFileExpected)
	}
	return true
}

func (st *visitState) span() Span {
	return Span{
		Offset:    st.s.Offset(),
		Length:    st.s.Length(),
		StartLine: st.s.StartLine(),
		StartCol:  st.s.StartColumn(),
		path:      st.path,
	}
}

// scanNext returns the next significant token, reporting scan errors and
// comments it passes.
func (st *visitState) scanNext() SyntaxKind {
	for {
		token := st.s.Scan()

		switch st.s.TokenError() {
		case ScanInvalidUnicode:
			st.handleError(InvalidUnicode)
		case ScanInvalidEscapeCharacter:
			st.handleError(InvalidEscapeCharacter)
		case ScanUnexpectedEndOfNumber:
			st.handleError(UnexpectedEndOfNumber)
		case ScanUnexpectedEndOfComment:
			st.handleError(UnexpectedEndOfComment)
		case ScanUnexpectedEndOfString:
			st.handleError(UnexpectedEndOfString)
		case ScanInvalidCharacter:
			st.handleError(InvalidCharacter)
		}

		switch token {
		case LineComment, BlockComment:
			if st.cfg.disallowComments {
				st.handleError(InvalidCommentToken)
			} else if st.v.OnComment != nil && st.skipped == 0 {
				st.v.OnComment(st.span())
			}
		case Unknown:
			st.handleError(InvalidSymbol)
		case Trivia, LineBreak:
		default:
			return token
		}
	}
}

// handleError reports code, then skips to the next token in skipUntil.
// Upstream also takes tokens to skip past, but only where parsing is already
// at EOF, so that's left out.
func (st *visitState) handleError(code ParseErrorCode, skipUntil ...SyntaxKind) {
	if st.v.OnError != nil && st.skipped == 0 {
		st.v.OnError(code, st.span())
	}
	if len(skipUntil) == 0 {
		return
	}
	for token := st.s.Token(); token != EOF && !slices.Contains(skipUntil, token); {
		token = st.scanNext()
	}
}

// container is an open object or array on parseValue's stack.
type container struct {
	array      bool
	needsComma bool
	index      int // array element, -1 before the first
}

// parseValue parses one value. Open containers go on st.open rather than the
// call stack, since Go can't recover from a stack overflow and upstream's
// recursion would hit one on deeply nested input.
func (st *visitState) parseValue() bool {
	if ok, opened := st.openValue(); !opened {
		return ok
	}
	for len(st.open) > 0 {
		c := &st.open[len(st.open)-1]
		closer := CloseBrace
		if c.array {
			closer = CloseBracket
		}
		if st.s.Token() == closer || st.s.Token() == EOF {
			st.close()
			continue
		}
		if st.s.Token() == Comma {
			if !c.needsComma {
				st.handleError(ValueExpected)
			}
			st.separator(",")
			st.scanNext()
			if st.s.Token() == closer && st.cfg.allowTrailingComma {
				st.close()
				continue
			}
		} else if c.needsComma {
			st.handleError(CommaExpected)
		}
		c.needsComma = true
		if c.array {
			st.parseElement(c)
		} else {
			st.parseProperty()
		}
	}
	return true
}

// openValue starts a container, or parses a literal; ok is false when there's
// no value there.
func (st *visitState) openValue() (ok, opened bool) {
	switch st.s.Token() {
	case OpenBracket:
		st.begin(st.v.OnArrayBegin)
		st.scanNext()
		st.open = append(st.open, container{array: true, index: -1})
		return true, true
	case OpenBrace:
		st.begin(st.v.OnObjectBegin)
		st.scanNext()
		st.open = append(st.open, container{})
		return true, true
	}
	return st.parseLiteral(), false
}

func (st *visitState) pushPath(seg any) {
	if st.withPath {
		st.path = st.path.push(seg)
	}
}

func (st *visitState) popPath() {
	if st.withPath {
		st.path = st.path.parent
	}
}

// close ends the innermost container.
func (st *visitState) close() {
	c := st.open[len(st.open)-1]
	st.open = st.open[:len(st.open)-1]
	if c.array {
		if c.index >= 0 {
			st.popPath()
		}
		st.end(st.v.OnArrayEnd)
		if st.s.Token() != CloseBracket {
			st.handleError(CloseBracketExpected)
		} else {
			st.scanNext()
		}
	} else {
		st.end(st.v.OnObjectEnd)
		if st.s.Token() != CloseBrace {
			st.handleError(CloseBraceExpected)
		} else {
			st.scanNext()
		}
	}
	// a container inside an object was a property's value, which ends here
	if n := len(st.open); n > 0 && !st.open[n-1].array {
		st.popPath()
	}
}

func (st *visitState) parseLiteral() bool {
	switch st.s.Token() {
	case NumericLiteral:
		raw := st.s.Value()
		var value any
		if st.cfg.useNumber {
			// "1." is 1, as upstream; json.Number("1.") won't marshal
			value = json.Number(strings.TrimSuffix(raw, "."))
			// Only a syntax error invalidates a json.Number. 1e400 overflows
			// float64 but the digits are still exact, which is the point of
			// UseNumber; encoding/json keeps it too.
			if _, err := strconv.ParseFloat(raw, 64); err != nil {
				var ne *strconv.NumError
				if !errors.As(err, &ne) || ne.Err != strconv.ErrRange {
					st.handleError(InvalidNumberFormat)
					value = json.Number("0")
				}
			}
		} else {
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				st.handleError(InvalidNumberFormat)
				f = 0
			}
			value = f
		}
		if st.v.OnLiteralValue != nil && st.skipped == 0 {
			st.v.OnLiteralValue(value, st.span())
		}
	case StringLiteral:
		if st.v.OnLiteralValue != nil && st.skipped == 0 {
			st.v.OnLiteralValue(st.s.Value(), st.span())
		}
	case NullKeyword:
		if st.v.OnLiteralValue != nil && st.skipped == 0 {
			st.v.OnLiteralValue(nil, st.span())
		}
	case TrueKeyword:
		if st.v.OnLiteralValue != nil && st.skipped == 0 {
			st.v.OnLiteralValue(true, st.span())
		}
	case FalseKeyword:
		if st.v.OnLiteralValue != nil && st.skipped == 0 {
			st.v.OnLiteralValue(false, st.span())
		}
	default:
		return false
	}
	st.scanNext()
	return true
}

func (st *visitState) separator(sep string) {
	if st.v.OnSeparator != nil && st.skipped == 0 {
		st.v.OnSeparator(sep, st.span())
	}
}

// parseProperty parses one member of the innermost object. If its value is a
// container, close ends the property instead.
func (st *visitState) parseProperty() {
	if st.s.Token() != StringLiteral {
		st.handleError(PropertyNameExpected, CloseBrace, Comma)
		st.handleError(ValueExpected, CloseBrace, Comma)
		return
	}
	name := st.s.Value()
	if st.v.OnObjectProperty != nil && st.skipped == 0 {
		st.v.OnObjectProperty(name, st.span())
	}
	// after the property event, which gets the object's path, as upstream
	st.pushPath(name)
	st.scanNext()

	if st.s.Token() == Colon {
		st.separator(":")
		st.scanNext()
		ok, opened := st.openValue()
		if opened {
			return
		}
		if !ok {
			st.handleError(ValueExpected, CloseBrace, Comma)
		}
	} else {
		st.handleError(ColonExpected, CloseBrace, Comma)
	}
	st.popPath()
}

// parseElement parses one element of the innermost array.
func (st *visitState) parseElement(c *container) {
	// the index goes on the path at the first element, as upstream
	if c.index++; c.index > 0 {
		st.popPath()
	}
	st.pushPath(c.index)
	if ok, opened := st.openValue(); !opened && !ok {
		st.handleError(ValueExpected, CloseBracket, Comma)
	}
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

	visit(text, Visitor{
		OnObjectBegin: func(Span) bool {
			push(map[string]any{})
			keys = append(keys, "")
			return true
		},
		OnObjectProperty: func(name string, _ Span) {
			keys[len(keys)-1] = name
		},
		OnObjectEnd: func(Span) {
			keys = keys[:len(keys)-1]
			pop()
		},
		OnArrayBegin: func(Span) bool {
			arr := make([]any, 0)
			push(&arr)
			return true
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
	}, false, opts...)

	if !hasRoot {
		return nil, errs
	}
	return result, errs
}
