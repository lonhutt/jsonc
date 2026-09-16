# go-jsonc: a Go port of node-jsonc-parser

Date: 2026-09-14
Status: approved, ready for implementation planning

## Purpose

A Go library for JSON with comments (JSONC) that parses fault-tolerantly, exposes
byte offsets for every token and node, and edits documents in place without
destroying comments or formatting.

It replicates the functionality of
[microsoft/node-jsonc-parser](https://github.com/microsoft/node-jsonc-parser),
the parser behind VS Code's settings and `tsconfig.json` handling.

`encoding/json` cannot do this job. It rejects comments and trailing commas,
reports no offsets, returns nothing usable on error, and cannot round-trip a
document. Every one of those is a requirement here.

## Scope

Full parity with upstream, including the edit layer: scanner, `Parse`, `Visit`,
`ParseTree`, location and navigation helpers, comment stripping, `Format`,
`Modify`, `Remove`, and `ApplyEdits`.

Upstream's string-interning cache is out of scope. It exists to fight V8
allocation costs; Go's string slicing makes it unnecessary.

## Module identity

```
module github.com/lonhutt/jsonc
```

A single module at the repository root, package `jsonc`, files flat in that
root. This satisfies Go's conventions on every axis: the last element of the
import path equals the package name, the package name is short and lowercase,
and callers write `jsonc.Parse` with no stutter.

The repository is named `jsonc` rather than `go-jsonc-parser`. The `go-` prefix
is a GitHub disambiguation habit, not a Go convention, and projects that use it
(`google/go-cmp/cmp`, `google/go-github/.../github`) put the package in a
subdirectory precisely to preserve the path-element rule. "Parser" is also
inaccurate for a library that formats and edits.

Three other Go packages use the identifier `jsonc`
(`muhammadmuzzammil1998`, `komkom`, `tidwall`). All are comment-strippers or
loose decoders; none has a CST, location API, or comment-preserving edits.
Import paths remain unique, and a consumer importing two of them aliases one, as
Go intends.

## Architecture

Upstream's layering is followed deliberately. Text is read exactly twice, never
more:

```
text ──> Scanner ──> Visit ──┬──> Parse      (value builder: stack of any)
                             └──> ParseTree  (node builder: stack of *Node)

text ──> Scanner ──> Format ──> []Edit        (second pass; keeps trivia)

Modify/Remove: text ──> ParseTree ──> locate edit range ──> Marshal value
                    ──> Format the snippet ──> []Edit ──> ApplyEdits ──> string
```

`Parse` and `ParseTree` are each a `Visitor` whose callbacks push and pop a
stack, roughly 60 lines apiece. Neither contains error handling; they record
what `Visit` reports.

The reason is parity. Fault-tolerance behavior — which errors are recovered,
where a bad token resyncs, what partial value survives — is the subtle part of
upstream and lives in exactly one place. A second, independent recursive-descent
parser for the tree would duplicate that logic, and every divergence between the
two would be a parity bug the ported tests might not catch.

`Format` is a separate scanner pass rather than a tree walk, because formatting
is about the whitespace and comments between tokens and the DOM discards those.

### Files

| File | Contents |
|---|---|
| `scanner.go` | `Scanner`, `SyntaxKind`, `ScanError`, token decoding |
| `parser.go` | `Visit`, `Parse`, `Visitor`, `Span`, `ParseError`, `ParseErrorCode`, options |
| `tree.go` | `Node`, `NodeType`, `ParseTree`, `FindNodeAtLocation`, `FindNodeAtOffset` |
| `location.go` | `Path`, `Location`, `GetLocation` |
| `strip.go` | `StripComments`, `ReplaceComments` |
| `format.go` | `FormatOptions`, `Range`, `Format` |
| `edit.go` | `Edit`, `ApplyEdits`, `Modify`, `Remove`, `ModifyOptions` |

## Public API

Everything not listed here is unexported.

### Scanner

```go
type SyntaxKind int
const (
    Unknown SyntaxKind = iota   // zero value is Unknown, not an invalid state
    OpenBrace; CloseBrace; OpenBracket; CloseBracket
    Comma; Colon
    NullKeyword; TrueKeyword; FalseKeyword
    StringLiteral; NumericLiteral
    LineComment; BlockComment; LineBreak; Trivia
    EOF
)
func (k SyntaxKind) String() string

type ScanError int
const (
    ScanNone ScanError = iota
    ScanUnexpectedEndOfComment; ScanUnexpectedEndOfString; ScanUnexpectedEndOfNumber
    ScanInvalidUnicode; ScanInvalidEscapeCharacter; ScanInvalidCharacter
)

type Scanner struct{ /* unexported */ }
type ScannerOption func(*scannerConfig)   // opaque; constructed by the funcs below

func NewScanner(text string, opts ...ScannerOption) *Scanner
func IgnoreTrivia() ScannerOption

func (s *Scanner) Scan() SyntaxKind    // advance, return the new token
func (s *Scanner) Token() SyntaxKind   // current token
func (s *Scanner) Value() string       // decoded string / raw number text
func (s *Scanner) Offset() int         // byte offset of current token
func (s *Scanner) Length() int
func (s *Scanner) Position() int       // byte offset just past current token
func (s *Scanner) SetPosition(pos int)
func (s *Scanner) StartLine() int      // 0-based
func (s *Scanner) StartColumn() int    // 0-based, bytes from line start
func (s *Scanner) TokenError() ScanError
```

### Parsing

```go
type ParseErrorCode int  // 1-based; zero is never a valid code
const (
    InvalidSymbol ParseErrorCode = iota + 1
    InvalidNumberFormat; PropertyNameExpected; ValueExpected; ColonExpected
    CommaExpected; CloseBraceExpected; CloseBracketExpected; EndOfFileExpected
    InvalidCommentToken; UnexpectedEndOfComment; UnexpectedEndOfString
    UnexpectedEndOfNumber; InvalidUnicode; InvalidEscapeCharacter; InvalidCharacter
)
func (c ParseErrorCode) String() string   // replaces printParseErrorCode

type ParseError struct {
    Code                ParseErrorCode
    Offset, Length      int
    StartLine, StartCol int
}
func (e ParseError) Error() string        // satisfies error

type ParseOption func(*parseConfig)   // opaque; constructed by the funcs below

func DisallowComments() ParseOption
func AllowTrailingComma() ParseOption
func AllowEmptyContent() ParseOption
func UseNumber() ParseOption              // literals decode as json.Number

func Parse(text string, opts ...ParseOption) (any, []ParseError)
func Visit(text string, v Visitor, opts ...ParseOption) bool
```

Upstream threads five positional arguments plus a `pathSupplier` closure through
nine callbacks. In Go that is a footgun, so they collapse into one value:

```go
type Span struct {
    Offset, Length      int
    StartLine, StartCol int
    Path                func() Path   // snapshotted at Span creation; never nil
}

type Visitor struct {                  // set only the callbacks you need
    OnObjectBegin, OnObjectEnd   func(Span)
    OnArrayBegin, OnArrayEnd     func(Span)
    OnObjectProperty             func(name string, s Span)
    OnLiteralValue               func(v any, s Span)
    OnSeparator                  func(sep string, s Span)
    OnComment                    func(Span)
    OnError                      func(code ParseErrorCode, s Span)
}
```

### Tree

```go
type NodeType string
const (
    NodeObject   NodeType = "object"
    NodeArray    NodeType = "array"
    NodeProperty NodeType = "property"
    NodeString   NodeType = "string"
    NodeNumber   NodeType = "number"
    NodeBoolean  NodeType = "boolean"
    NodeNull     NodeType = "null"
)

type Node struct {
    Type           NodeType
    Literal        any   // scalar nodes only; nil for object/array/property
    Offset, Length int
    ColonOffset    int   // property nodes; -1 otherwise
    Parent         *Node
    Children       []*Node
}
func (n *Node) Value() any   // deep-materialize this subtree (was getNodeValue)
func (n *Node) Path() Path   // was getNodePath

func ParseTree(text string, opts ...ParseOption) (*Node, []ParseError)
func FindNodeAtLocation(root *Node, path Path) *Node
func FindNodeAtOffset(root *Node, offset int, includeRightBound bool) *Node
```

The scalar field is `Literal` because `Value` is a method and Go cannot have
both under one name.

### Location

```go
type Path []any   // elements are string (property) or int (index)

type Location struct {
    PreviousNode    *Node
    Path            Path
    IsAtPropertyKey bool
}
func (l Location) Matches(pattern Path) bool   // "*" wildcard, as upstream
func GetLocation(text string, offset int) Location
```

A `Path` element must be a `string` (property name) or an `int` (array index).
How a malformed element is reported depends on the receiving function's
signature, and each is specified here so the behavior is not left to the
implementer:

- `Modify`, `Remove` — return a non-nil `error`.
- `FindNodeAtLocation` — returns `nil`, identical to a path that simply does not
  resolve.
- `Location.Matches` — returns `false`.

### Comments

```go
func StripComments(text string) string             // removes them
func ReplaceComments(text string, ch rune) string  // blanks them, preserving offsets
```

### Editing

```go
type Range struct{ Offset, Length int }
type Edit  struct{ Offset, Length int; Content string }

type FormatOptions struct {
    TabSize            int    // 0 -> 4
    InsertSpaces       bool
    EOL                string // "" -> detected from text, else "\n"
    InsertFinalNewline bool
    KeepLines          bool
}
func DefaultFormatOptions() FormatOptions   // {4, true, "\n", false, false}

func Format(text string, r *Range, o FormatOptions) []Edit   // nil r = whole document
func ApplyEdits(text string, edits []Edit) (string, error)   // errors on overlap

type ModifyOptions struct {
    Format            *FormatOptions
    IsArrayInsertion  bool
    GetInsertionIndex func(properties []string) int
}
func Modify(text string, path Path, value any, o ModifyOptions) ([]Edit, error)
func Remove(text string, path Path, o ModifyOptions) ([]Edit, error)
```

Upstream deletes by passing `undefined` as the value. Go has no `undefined`, and
`nil` legitimately means JSON `null`, so deletion gets its own function.
Overloading `Modify` with a sentinel would make `Modify(text, path, nil, o)`
ambiguous in exactly the case where guessing wrong silently destroys a config
entry.

Values passed to `Modify` go through `encoding/json.Marshal`, mirroring
upstream's `JSON.stringify`. Anything with a marshaler works; unmarshalable
values return an error.

## Scanner internals

No allocation for tokens that do not need decoding: `Value()` returns a slice of
the input for numbers and for strings without escapes, allocating only when a
string contains `\`.

`\uXXXX` pairs decode through `utf16.DecodeRune`, so surrogate pairs become one
UTF-8 rune. A lone or malformed surrogate sets `ScanInvalidUnicode` and emits
`U+FFFD` — a value alongside the error, matching upstream rather than bailing.

Line tracking is incremental. The scanner keeps `lineNumber` and
`lineStartOffset`, updating them as it consumes `\n`, `\r`, and `\r\n`.
`StartColumn()` is `offset - lineStartOffset`: O(1) per token, no rescanning.

## Fault tolerance

Recovery lives in `Visit` and nowhere else. Three rules, from upstream:

1. **Every error is reported and recovery continues.** `Visit` never stops at the
   first problem. `Parse` always returns a best-effort value; `ParseTree` always
   returns a best-effort tree. A caller wanting strictness checks
   `len(errs) == 0` — that is the contract, not a returned `error`.
2. **Errors carry a skip set.** Each reported error supplies two token lists:
   tokens to accept as the recovery point, and tokens never to consume. The
   parser skips forward until it hits one, so a missing comma inside an object
   resyncs at the next property instead of discarding the rest of the document.
3. **Structural closers are synthesized.** An unterminated object still fires
   `OnObjectEnd`, with the error recorded, so the node stack always unwinds and
   `ParseTree` cannot return a half-built tree with dangling parents.

`Visit` returns `false` only when a visitor callback signals a hard stop, never
on parse errors.

### Error handling boundaries

Three mechanisms for three distinct situations:

- `[]ParseError` — recoverable, expected, plural. Returned alongside a usable
  result from `Parse` and `ParseTree`.
- `ScanError` — per-token state on `Scanner`, for callers driving tokenization
  directly.
- `error` — only from `ApplyEdits`, `Modify`, and `Remove`, which have
  all-or-nothing failures: overlapping edits, a malformed `Path` element, an
  unmarshalable value, an unresolvable path. There is no meaningful partial
  result for these.

## Concurrency

Every top-level function is pure: text in, values out, no shared state. All are
safe to call concurrently.

A `*Scanner` is stateful and is not safe for concurrent use.

A `*Node` tree is immutable after construction and safe to read concurrently.
This is stated explicitly in the docs because the `Parent` pointers make the
tree look mutable.

## Format

`Format` is a whitespace-only rewriter. It scans with trivia enabled and, for
each gap between two tokens, computes what belongs there — a line break plus
indentation, a single space, or nothing — from the current depth and the token
pair. It emits an `Edit` only where the existing gap differs.

Minimality matters: formatting an already-correct document produces zero edits,
so a caller applying edits to a document with a cursor in it does not see the
whole file replaced.

Rules that are not obvious:

- **Comments pin line breaks.** A line comment forces a newline after it;
  collapsing one would comment out the following token, turning formatting into
  data loss. A block comment sharing a line with a value stays on that line.
- **Range formatting expands to line boundaries.** A supplied `Range` widens
  backward to the start of its first line, and the initial indent level is
  derived by scanning the text before it. Otherwise re-indenting a nested block
  would compute depth 0 and flatten it.
- **The document's own line endings always win.** `Format` uses the first line
  terminator found in the text. `FormatOptions.EOL` applies ONLY when the
  document contains no terminator at all (a single-line file), and `"\n"` is the
  final fallback. So formatting a CRLF file with `EOL: "\n"` keeps CRLF.
  This is upstream's `getEOL` behavior and we match it deliberately: a file's
  existing endings are a stronger signal than a caller default, and emitting
  mixed endings inside one file is worse than ignoring a preference.
  Consequence worth knowing: `Format` cannot be used to CONVERT line endings.
  Normalize the text first if that is what you want.
- **`KeepLines` preserves the author's blank lines and breaks**, normalizing only
  indentation. It is how format-on-save avoids fighting deliberate grouping in a
  config file.

## Modify and Remove

`Modify` computes a splice, then formats only that splice. It parses the tree,
finds the **parent** of the target path, and picks one of five cases:

| Case | Edit |
|---|---|
| empty path | replace the whole document |
| object, key exists | replace the value node's range |
| object, key absent | insert `"key": value` at the chosen index, with comma placement |
| array, index in range | replace that element, or insert before it if `IsArrayInsertion` |
| array, index `-1` or `len` | append |

Insertion index comes from `GetInsertionIndex(properties)` when set, otherwise
the end. The marshaled value is re-indented to the depth of its insertion point
and run through `Format` over the affected range, so an inserted nested object
arrives matching the surrounding style rather than as one long line.

**Comma placement** is the fiddly part and gets the heaviest testing. Inserting
into an empty container needs no comma; inserting at the end needs one before;
inserting at the front needs one after; inserting into a container whose last
element already carries a trailing comma needs neither. `Remove` is the mirror
image: it must take the node and exactly one adjacent comma, preferring the
preceding one, or it leaves behind `{"a": 1, }` or `{,}`.

**`Remove` does not delete adjacent comments.** A line comment above a removed
property stays. Whether a comment belongs to the node below or the node above is
unanswerable from syntax alone, and silently deleting a user's comment is worse
than leaving an orphan. Upstream behaves the same way; this is documented as
intentional so it does not read as a bug.

`ApplyEdits` sorts edits by descending offset and applies them back-to-front, so
offsets stay valid without bookkeeping. It validates that no two edits overlap
and rebuilds the string with a single `strings.Builder` pre-sized to the final
length. Overlap is an `error`, not silent last-write-wins.

### Round-trip invariant

For any input that parses without errors:

- `ApplyEdits(text, Format(text, nil, o))` parses to a value deep-equal to the
  original, and
- formatting the result a second time produces zero edits.

This is fuzzed directly.

## Testing

Upstream has four test files totaling ~58KB: `json.test.ts` (36KB),
`format.test.ts` (12KB), `edit.test.ts` (9KB), and `string-intern.test.ts`
(an implementation detail we do not have and will not port).

The ported suite mirrors upstream file-for-file, so future upstream changes can
be diffed against ours:

| Go file | Ported from | Covers |
|---|---|---|
| `scanner_test.go` | `json.test.ts` (scanner) | token kinds, values, escapes, scan errors, line/col |
| `parser_test.go` | `json.test.ts` (rest) | `Parse`, `Visit`, `ParseTree`, errors, options |
| `location_test.go` | `json.test.ts` (location) | `GetLocation`, `Matches`, `FindNodeAt*` |
| `format_test.go` | `format.test.ts` | `Format`, ranges, `KeepLines`, EOL |
| `edit_test.go` | `edit.test.ts` | `Modify`, `Remove`, `ApplyEdits` |

Each is a table test carrying the upstream input and expected output as literal
data. No shared helper cleverness: when a parity test fails, the message should
show the exact input rather than a builder call.

Upstream's assertion helpers translate directly — `assertKinds(text, ...kinds)`
and `assertLocation(text, segments, nodeType, isAtPropertyKey)` become Go
helpers of the same shape. Edit tests follow upstream's `assertEdit(content,
edits, expected)` pattern, comparing the resulting string rather than the exact
edit decomposition, so a different-but-equivalent edit set still passes.

### Fuzzing

Coverage upstream does not have, and the reason byte offsets are safe: every
offset bug in this design is a slicing bug, and a slicing bug on a multi-byte
rune is a panic. A fuzzer finds those in seconds; an ASCII test suite never
will.

- `FuzzParse` — never panics; every `ParseError` has `Offset`/`Length` within
  bounds; a clean parse deep-equals `encoding/json` on inputs that are also
  valid strict JSON.
- `FuzzFormatIdempotent` — the round-trip invariant above.
- `FuzzModifyApply` — after `Modify`, reading the path back yields the value
  written.
- `FuzzScanner` — token offsets are monotonic, non-overlapping, and tile the
  input exactly.

Seed corpora come from the ported test inputs.

### CI

`go test`, `go vet`, `staticcheck`, `-race`, and a bounded `-fuzztime=30s` per
target. Long fuzzing campaigns stay manual.

An `Example` function per exported entry point, doubling as pkg.go.dev
documentation.

## Divergences from node-jsonc-parser

These belong in a README section, not buried in code comments. Anyone porting JS
code to this library will hit them.

1. **Offsets and columns are bytes, not UTF-16 code units.** Identical for
   ASCII; different for any non-ASCII content. Consumers needing LSP positions
   convert at the boundary.
2. **Deletion is `Remove`, not `Modify(..., nil, ...)`.**
3. **`Node.Literal` field, `Node.Value()` method.**
4. **`Parse` does not preserve object key order**, because `map[string]any`
   cannot. `ParseTree` preserves it in `Children`; that is the supported route
   when order matters.
5. **Numbers are `float64` by default**, matching upstream's JS semantics, with
   `UseNumber()` for exact large integers — a gap upstream has no answer for.
6. **An out-of-range number is an error, not `Infinity`.** `1e400` parses to
   `0` with `InvalidNumberFormat` reported, where upstream's `Number()` yields
   `Infinity`. `encoding/json` rejects it too, and `+Inf` cannot be marshalled
   back out, which would break `Modify`'s round-trip. With `UseNumber()` the
   literal is preserved exactly and no error is reported.
7. **No string interning.**
8. **Options are variadic functions** (`ParseOption`) rather than partial-option
   objects. `FormatOptions` stays a struct: five independent knobs, not a set of
   flags.

## Deliberate non-goals

- A range-over-func token iterator (`for tok := range jsonc.Tokens(src)`). New
  API surface with no upstream counterpart; the `Scanner.Scan()` loop covers it.
  Straightforward to add later if it is actually wanted.
- JSON5 support (unquoted keys, single quotes, hex numbers). JSONC is a distinct,
  narrower format, and that is what upstream implements.
- A schema validator.
- Unmarshaling into structs. `StripComments` composed with `encoding/json`
  already serves that, and the tolerant path is what this library is for.
