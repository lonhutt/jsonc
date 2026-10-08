package jsonc

import "strings"

// Range is a byte range in a document.
type Range struct {
	Offset, Length int
}

// FormatOptions controls Format. The zero value is usable: TabSize 0 means 4,
// and an empty EOL means the terminator is taken from the document.
type FormatOptions struct {
	TabSize      int
	InsertSpaces bool

	// EOL is the line terminator to insert. It is only a fallback: a document
	// that already contains a terminator is formatted with that one, so a CRLF
	// file stays CRLF whatever EOL says. An empty EOL with a document that has
	// no terminator at all means "\n".
	EOL string

	// InsertFinalNewline ends a document that has at least one token with
	// exactly one EOL. Without it, trailing whitespace is removed entirely.
	// KeepLines overrides both: trailing line breaks the author wrote are kept.
	InsertFinalNewline bool

	// KeepLines preserves the author's line breaks, blank lines included, and
	// normalizes only indentation. Tokens the author put on one line stay on
	// one line, separated by a single space -- so `{"a":1}` becomes
	// `{ "a": 1 }`, as it does upstream.
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

// eolFor returns the first terminator in text, or the configured EOL, or "\n".
//
// The document wins over the option. That is upstream's precedence and it is
// deliberate: reformatting must not silently rewrite every line ending of a
// file whose terminator differs from the caller's default.
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

// formatter holds the state of one Format pass.
type formatter struct {
	text       string
	o          FormatOptions
	eol        string
	indentUnit string

	// hasRange reports whether edits are filtered; edits that do not overlap
	// [rangeStart, rangeEnd) are discarded.
	hasRange             bool
	rangeStart, rangeEnd int
	// pastEnd reports that the requested range ran beyond the text. rangeEnd
	// is clamped, but upstream's is not, so its range then still overlaps an
	// insertion AT the end of the text: the final newline.
	pastEnd bool

	// scanText is the slice of text being scanned: for a range, the lines the
	// range touches. delta is its offset within text, so scanner offsets plus
	// delta are document offsets.
	scanText string
	delta    int

	s     *Scanner
	edits []Edit

	// numberLineBreaks is how many line terminators preceded the current
	// token: at most 1 unless KeepLines, which needs the real count.
	numberLineBreaks int
	indentLevel      int
	initialIndent    int

	// hasError suppresses the edit for one token pair: it is recomputed on
	// every scan, and set where collapsing the gap would join two tokens.
	hasError bool
}

// Format returns the edits that reformat text. Passing a nil Range formats the
// whole document; otherwise only edits overlapping the range are returned, with
// the range widened to whole lines so nesting depth and the initial indent are
// computed from the text that precedes it.
//
// Only whitespace between tokens is changed. An already-formatted document
// produces no edits.
//
// A nil Range is NOT the same as &Range{0, len(text)}. The whole-document form
// formats from indent level 0; the range form derives its initial indent from
// the leading whitespace of the range's first line, so a document that starts
// with indentation is re-indented one level deeper:
//
//	Format("     {\"a\":1}", nil, o)                 -> "{\n  \"a\": 1\n}"
//	Format("     {\"a\":1}", &Range{0, 12}, o)       -> "    {\n      \"a\": 1\n    }"
//
// This mirrors node-jsonc-parser's own split between the two branches. Pass nil
// when you mean the whole document.
//
// NOTE: total edit content is O(depth^2) -- each of O(depth) gaps carries an
// O(depth) indent string. Fine for human-authored documents; a 20,000-deep
// document produces 39,998 edits holding ~800MB of content at a 2-space indent,
// and that scales with the indent width. A service formatting untrusted input
// should bound nesting depth before calling Format.
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
		// Clamp the length before adding it, not after: rangeStart + MaxInt
		// wraps negative, and clamping the wrapped sum would silently collapse
		// the range to nothing where the caller plainly meant "to the end".
		f.rangeEnd = f.rangeStart + clamp(r.Length, 0, len(text)-f.rangeStart)
		f.pastEnd = r.Length > len(text)-f.rangeStart

		// Widen backward to the start of the line: the initial indent has to be
		// read from the text in front of the range, or a nested block would be
		// re-indented as though it were at depth 0.
		start := f.rangeStart
		for start > 0 && !isLineBreak(text[start-1]) {
			start--
		}
		// Widen forward to the end of the line, so the last gap in the range is
		// scanned in full and the trailing edit stops there.
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

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// computeIndentLevel counts the leading indentation of line as indent units.
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

// addEdit records a replacement of text[start:end] with content, unless the
// document already says exactly that, the edit falls outside the range, or the
// surrounding tokens are malformed.
func (f *formatter) addEdit(content string, start, end int) {
	if f.hasError {
		return
	}
	if f.hasRange && !((start < f.rangeEnd || f.pastEnd && start == len(f.text)) && end > f.rangeStart) {
		return
	}
	if start < 0 || end > len(f.text) || start > end {
		return
	}
	// Minimality: an edit that changes nothing must not be emitted, or every
	// caller applying these edits rewrites text it did not need to touch.
	if f.text[start:end] == content {
		return
	}
	f.edits = append(f.edits, Edit{Offset: start, Length: end - start, Content: content})
}

// scanNext advances past trivia, recording whether it crossed a line break.
func (f *formatter) scanNext() SyntaxKind {
	token := f.s.Scan()
	f.numberLineBreaks = 0
	for token == Trivia || token == LineBreak {
		if token == LineBreak {
			if f.o.KeepLines {
				f.numberLineBreaks += countLineBreaks(f.scanText[f.s.Offset() : f.s.Offset()+f.s.Length()])
			} else {
				f.numberLineBreaks = 1
			}
		}
		token = f.s.Scan()
	}
	f.hasError = token == Unknown || f.s.TokenError() != ScanNone
	return token
}

// indent returns level indent units, never a negative repeat: unbalanced
// closing brackets drive the level below zero.
func (f *formatter) indent(level int) string {
	if level <= 0 {
		return ""
	}
	return strings.Repeat(f.indentUnit, level)
}

// countLineBreaks counts the terminators in a LineBreak token. Our scanner
// emits one token per RUN of terminators where upstream emits one per
// terminator, so KeepLines has to count inside the token or every blank line
// is lost. "\r\n" is one terminator.
func countLineBreaks(run string) int {
	n := 0
	for i := 0; i < len(run); i++ {
		if run[i] == '\n' || i+1 == len(run) || run[i+1] != '\n' {
			n++
		}
	}
	return n
}

func (f *formatter) newLinesAndIndent() string {
	return strings.Repeat(f.eol, max(f.numberLineBreaks, 1)) + f.indent(f.initialIndent+f.indentLevel)
}

// tokenStart and tokenEnd are document offsets for the current token.
func (f *formatter) tokenStart() int { return f.s.Offset() + f.delta }
func (f *formatter) tokenEnd() int   { return f.s.Offset() + f.s.Length() + f.delta }

func (f *formatter) run() {
	firstToken := f.scanNext()
	if firstToken == EOF {
		return
	}

	// Indentation before the very first token, plus, with KeepLines, the
	// author's leading line breaks.
	//
	// Upstream inserts those breaks at offset 0 as a separate edit, which goes
	// wrong two ways we do not copy: an already-formatted document gets a no-op
	// insert/delete pair at the same offset on every pass, and a document with
	// no tokens gets its breaks inserted again each time it is formatted.
	// One edit over the whole gap gives the same text for every document that
	// has a token. Like upstream, a range never keeps leading breaks: its
	// insertion at offset 0 can never overlap a range.
	leading := f.indent(f.initialIndent)
	if f.o.KeepLines && f.numberLineBreaks > 0 && !f.hasRange {
		leading = strings.Repeat(f.eol, f.numberLineBreaks) + leading
	}
	f.addEdit(leading, f.delta, f.tokenStart())

	for firstToken != EOF {
		firstEnd := f.tokenEnd()
		secondToken := f.scanNext()

		replaceContent := ""
		needsLineBreak := false

		// Comments sharing a line with the previous token keep a single space
		// before them and stay on that line. A line comment forces a break
		// after it: without one, everything that followed on the rewritten
		// line would be commented out.
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

		// keptBreak: KeepLines is on and the author broke the line here, so
		// the break stays whatever the token rules below would have said.
		keptBreak := f.o.KeepLines && f.numberLineBreaks > 0
		// separator is the gap after '{', '[' or ',': a line break normally;
		// with KeepLines, the author's break or a single space.
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
			// An empty container stays on one line: `{}`, or `{ }` with
			// KeepLines.
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
					// Two values with nothing but whitespace between them is
					// malformed input. Removing that whitespace would join
					// them into one token, so leave the gap alone.
					f.hasError = true
				}
			case Unknown:
				f.hasError = true
			}
			// A comment that was on a line of its own stays on one. This
			// deliberately overrides whatever the switch above decided: the
			// firstToken rules answer "what separates these two tokens",
			// which for a value followed by a comment is a single space --
			// right for a trailing comment, wrong for a comment the author
			// put on its own line. Only this check can tell them apart,
			// because only it looks at what was between them.
			if f.numberLineBreaks > 0 && (secondToken == LineComment || secondToken == BlockComment) {
				replaceContent = f.newLinesAndIndent()
			}
		}

		if secondToken == EOF {
			switch {
			case keptBreak:
				replaceContent = f.newLinesAndIndent()
			case f.o.InsertFinalNewline:
				replaceContent = f.eol
			default:
				replaceContent = ""
			}
		}

		f.addEdit(replaceContent, firstEnd, f.tokenStart())
		firstToken = secondToken
	}
}
