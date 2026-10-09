package jsonc

import "strings"

// Range is a byte range in a document.
type Range struct {
	Offset, Length int
}

// FormatOptions controls Format. The zero value works: TabSize 0 means 4,
// and an empty EOL means use the document's.
type FormatOptions struct {
	TabSize      int
	InsertSpaces bool

	// EOL is only a fallback; a document that already has a terminator keeps
	// it, so a CRLF file stays CRLF. Empty, with no terminator in the document,
	// means "\n".
	EOL string

	// InsertFinalNewline ends a document that has a token with exactly one EOL;
	// without it, trailing whitespace is removed. KeepLines overrides both and
	// keeps the author's trailing breaks.
	InsertFinalNewline bool

	// KeepLines keeps the author's line breaks, blank lines included, and only
	// fixes indentation. Tokens on one line get a single space between them, so
	// `{"a":1}` becomes `{ "a": 1 }`; same as upstream.
	KeepLines bool
}

// DefaultFormatOptions returns 4-space indentation with LF line endings.
func DefaultFormatOptions() FormatOptions {
	return FormatOptions{TabSize: 4, InsertSpaces: true, EOL: "\n"}
}

func (o FormatOptions) tabSize() int {
	if o.TabSize <= 0 {
		return 4
	}
	return o.TabSize
}

func (o FormatOptions) indentUnit() string {
	if o.InsertSpaces {
		return strings.Repeat(" ", o.tabSize())
	}
	return "\t"
}

// eolFor returns the first terminator in text, else the configured EOL, else
// "\n". The document wins, as upstream; formatting shouldn't rewrite every line
// ending in a file.
func (o FormatOptions) eolFor(text string) string {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				return "\r\n"
			}
			return "\r"
		case '\n':
			return "\n"
		}
	}
	if o.EOL != "" {
		return o.EOL
	}
	return "\n"
}

type formatter struct {
	text       string
	o          FormatOptions
	eol        string
	indentUnit string

	// hasRange drops edits outside [rangeStart, rangeEnd).
	hasRange             bool
	rangeStart, rangeEnd int
	// pastEnd: the requested range ran past the text. Upstream doesn't clamp,
	// so its range still covers an insertion at the very end (the final newline).
	pastEnd bool

	// scanText is the lines a range touches, or all of text; delta is its
	// offset in text.
	scanText string
	delta    int

	s     *Scanner
	edits []Edit

	// line terminators before the current token; at most 1 unless KeepLines
	numberLineBreaks int
	indentLevel      int
	initialIndent    int

	// hasError suppresses the edit for one token pair; set when collapsing the
	// gap would join two tokens.
	hasError bool
}

// Format returns the edits that reformat text, or with r, the lines r touches.
// Only whitespace between tokens changes. A range takes its starting indent
// from its first line, so it isn't the same as a nil r.
func Format(text string, r *Range, o FormatOptions) []Edit {
	f := &formatter{
		text:       text,
		o:          o,
		eol:        o.eolFor(text),
		indentUnit: o.indentUnit(),
	}

	if r == nil {
		f.rangeStart, f.rangeEnd = 0, len(text)
		f.scanText, f.delta = text, 0
		f.initialIndent = 0
	} else {
		f.hasRange = true
		f.rangeStart = clamp(r.Offset, 0, len(text))
		// The end stays where the caller put it, so a start before the text
		// doesn't move it. Offset+Length can wrap, so test the length against
		// what's left instead of adding.
		length, asked := max(r.Length, 0), 0
		switch {
		case r.Offset < 0:
			asked = r.Offset + length
		case length > len(text)-min(r.Offset, len(text)):
			asked = len(text) + 1
		default:
			asked = r.Offset + length
		}
		f.rangeEnd = clamp(asked, f.rangeStart, len(text))
		f.pastEnd = asked > len(text)

		// Widen back to the line start; the starting indent comes from there, or a
		// nested block would be re-indented as depth 0.
		start := lineStart(text, f.rangeStart)
		// Widen to the line end, so the last gap is scanned in full.
		end := f.rangeEnd
		for end < len(text) && !isLineBreak(text[end]) {
			end++
		}
		f.scanText, f.delta = text[start:end], start
		f.initialIndent = computeIndentLevel(f.scanText, o)
	}

	f.s = NewScanner(f.scanText)
	f.run()
	return f.edits
}

// lineStart returns the start of offset's line, moved back to the start of a
// token the line would cut off: one it starts inside (a block comment, or a
// string an escaped line break keeps going), which scanning from mid-token
// misreads, or an unterminated string ending right there, whose line break
// mustn't be collapsed. Upstream does neither.
func lineStart(text string, offset int) int {
	for {
		for offset > 0 && !isLineBreak(text[offset-1]) {
			offset--
		}
		if offset == 0 {
			return 0
		}
		// Only a block comment or an escaped line break lets a token reach
		// across a line start; without either before it, skip the scan.
		if before := text[:offset]; !strings.Contains(before, "/*") && !strings.Contains(before, "\\\n") && !strings.Contains(before, "\\\r") {
			return offset
		}
		s := NewScanner(text)
		k := s.Scan()
		prev, prevOpen := 0, false
		for k != EOF && s.Offset()+s.Length() <= offset {
			prev, prevOpen = s.Offset(), k == StringLiteral && s.TokenError() == ScanUnexpectedEndOfString
			k = s.Scan()
		}
		switch {
		case prevOpen && k != EOF && s.Offset() == offset:
			offset = prev
		// A range starting between \r and \n only splits whitespace; upstream
		// leaves that alone too.
		case k == EOF || k == LineBreak || s.Offset() >= offset:
			return offset
		default:
			offset = s.Offset()
		}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// computeIndentLevel counts line's leading indentation in indent units.
func computeIndentLevel(line string, o FormatOptions) int {
	tabSize := o.tabSize()
	columns := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			columns++
		case '\t':
			columns += tabSize
		default:
			return columns / tabSize
		}
	}
	return columns / tabSize
}

// addEdit skips an edit that's outside the range, next to a malformed token,
// or changes nothing.
func (f *formatter) addEdit(content string, start, end int) {
	if f.hasError {
		return
	}
	if f.hasRange && !((start < f.rangeEnd || f.pastEnd && start == len(f.text)) && end > f.rangeStart) {
		return
	}
	if f.text[start:end] == content {
		return
	}
	f.edits = append(f.edits, Edit{Offset: start, Length: end - start, Content: content})
}

// scanNext skips trivia, counting line breaks on the way.
func (f *formatter) scanNext() SyntaxKind {
	token := f.s.Scan()
	f.numberLineBreaks = 0
	for token == Trivia || token == LineBreak {
		if token == LineBreak {
			if f.o.KeepLines {
				f.numberLineBreaks++
			} else {
				f.numberLineBreaks = 1
			}
		}
		token = f.s.Scan()
	}
	f.hasError = token == Unknown || f.s.TokenError() != ScanNone
	return token
}

// indent never repeats a negative count; extra closing brackets push level
// below zero.
func (f *formatter) indent(level int) string {
	if level <= 0 {
		return ""
	}
	return strings.Repeat(f.indentUnit, level)
}

func (f *formatter) newLinesAndIndent() string {
	return strings.Repeat(f.eol, max(f.numberLineBreaks, 1)) + f.indent(f.initialIndent+f.indentLevel)
}

func (f *formatter) tokenStart() int { return f.s.Offset() + f.delta }
func (f *formatter) tokenEnd() int   { return f.s.Offset() + f.s.Length() + f.delta }

func (f *formatter) run() {
	firstToken := f.scanNext()
	if firstToken == EOF {
		return
	}

	// The first token's indent, plus the leading breaks with KeepLines.
	//
	// Upstream emits the breaks as a separate insert at offset 0, which never
	// settles: a formatted document gets a no-op insert/delete pair every pass,
	// and one with no tokens gains its breaks again each time. One edit over the
	// gap gives the same text otherwise. Like upstream, a range drops them; an
	// insert at 0 never overlaps a range.
	leading := f.indent(f.initialIndent)
	if f.o.KeepLines && f.numberLineBreaks > 0 && !f.hasRange {
		leading = strings.Repeat(f.eol, f.numberLineBreaks) + leading
	}
	f.addEdit(leading, f.delta, f.tokenStart())

	for firstToken != EOF {
		firstEnd := f.tokenEnd()
		// An unterminated string ends at the line break after it; collapsing
		// that gap pulls the next token into the string. Upstream does.
		openString := firstToken == StringLiteral && f.s.TokenError() == ScanUnexpectedEndOfString
		secondToken := f.scanNext()

		replaceContent := ""
		needsLineBreak := false

		// A comment on the same line as the previous token stays there after one
		// space. A line comment forces a break after it, or whatever follows gets
		// commented out.
		for f.numberLineBreaks == 0 && (secondToken == LineComment || secondToken == BlockComment) {
			f.addEdit(" ", firstEnd, f.tokenStart())
			firstEnd = f.tokenEnd()
			needsLineBreak = secondToken == LineComment
			if needsLineBreak {
				replaceContent = f.newLinesAndIndent()
			} else {
				replaceContent = ""
			}
			secondToken = f.scanNext()
		}

		// keptBreak: KeepLines, and the author broke the line here.
		keptBreak := f.o.KeepLines && f.numberLineBreaks > 0
		// separator is the gap after '{', '[' or ','; a break, or with KeepLines
		// the author's break or a space.
		separator := func() string {
			if keptBreak || !f.o.KeepLines {
				return f.newLinesAndIndent()
			}
			return " "
		}

		switch secondToken {
		case CloseBrace, CloseBracket:
			opener := OpenBrace
			if secondToken == CloseBracket {
				opener = OpenBracket
			}
			if firstToken != opener {
				f.indentLevel--
			}
			// an empty container stays on one line: `{}`, or `{ }` with KeepLines
			if keptBreak || !f.o.KeepLines && firstToken != opener {
				replaceContent = f.newLinesAndIndent()
			} else if f.o.KeepLines {
				replaceContent = " "
			}
		default:
			switch firstToken {
			case OpenBrace, OpenBracket:
				f.indentLevel++
				replaceContent = separator()
			case Comma:
				replaceContent = separator()
			case LineComment:
				replaceContent = f.newLinesAndIndent()
			case BlockComment:
				if f.numberLineBreaks > 0 {
					replaceContent = f.newLinesAndIndent()
				} else if !needsLineBreak {
					replaceContent = " "
				}
			case Colon:
				if keptBreak {
					replaceContent = f.newLinesAndIndent()
				} else if !needsLineBreak {
					replaceContent = " "
				}
			case StringLiteral:
				if keptBreak {
					replaceContent = f.newLinesAndIndent()
				} else if secondToken == Colon && !needsLineBreak {
					replaceContent = ""
				}
			case NullKeyword, TrueKeyword, FalseKeyword, NumericLiteral,
				CloseBrace, CloseBracket:
				if keptBreak {
					replaceContent = f.newLinesAndIndent()
				} else if (secondToken == LineComment || secondToken == BlockComment) && !needsLineBreak {
					replaceContent = " "
				} else if secondToken != Comma && secondToken != EOF {
					// Two values separated only by whitespace is malformed; collapsing
					// the gap would join them, so leave it.
					f.hasError = true
				}
			case Unknown:
				f.hasError = true
			}
			// A comment on its own line stays on its own line. This overrides the
			// switch above, which gives a value-then-comment pair one space; right
			// for a trailing comment, wrong here.
			if f.numberLineBreaks > 0 && (secondToken == LineComment || secondToken == BlockComment) {
				replaceContent = f.newLinesAndIndent()
			}
		}

		if secondToken == EOF {
			switch {
			case keptBreak:
				replaceContent = f.newLinesAndIndent()
			// A range's scan stops at its last line's end, which may not be
			// the document's; upstream puts the final newline there anyway.
			case f.o.InsertFinalNewline && f.delta+len(f.scanText) == len(f.text):
				replaceContent = f.eol
			default:
				replaceContent = ""
			}
		}

		if openString && secondToken != EOF {
			f.hasError = true
		}
		f.addEdit(replaceContent, firstEnd, f.tokenStart())
		firstToken = secondToken
	}
}
