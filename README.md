# jsonc

A Go library for JSON with comments (JSONC), the format VS Code settings and
`tsconfig.json` use. It's a port of Microsoft's
[node-jsonc-parser](https://github.com/microsoft/node-jsonc-parser), the parser
behind those files, and keeps its behavior: fault-tolerant parsing, byte
offsets for every token and node, and edits that leave comments and formatting
alone.

```sh
go get github.com/lonhutt/jsonc
```

No dependencies outside the standard library.

## Why not encoding/json

`encoding/json` rejects comments and trailing commas, reports no offsets,
gives you nothing usable when the input is broken, and can't change one value
in a document without rewriting the whole thing. A settings file someone is
halfway through typing needs all four.

## Parsing

`Parse` gives you the same values `json.Unmarshal` into an `any` would. It
never stops at the first problem; you get a best-effort value back along with
every error, so check `len(errs) == 0` if you want to be strict.

```go
text := `{
  // the server to talk to
  "host": "localhost",
  "port": 8080,
}`
v, errs := jsonc.Parse(text, jsonc.AllowTrailingComma())
fmt.Println(len(errs), v.(map[string]any)["port"]) // 0 8080

v, errs = jsonc.Parse(`{"a": 1 "b": 2}`)
fmt.Println(v, errs) // map[a:1 b:2] [CommaExpected at offset 8 (line 0, column 8)]
```

When you need positions, or key order, `ParseTree` builds a tree of nodes with
offsets, and `FindNodeAtLocation` walks it by path:

```go
root, _ := jsonc.ParseTree(`{"a": [1, 2]}`)
node := jsonc.FindNodeAtLocation(root, jsonc.Path{"a", 1})
fmt.Println(node.Literal, node.Offset) // 2 10
```

`Visit` is the callback API both of these are built on, and `NewScanner` gets
you the tokens.

## Editing

`Modify` and `Remove` don't return a new document; they return edits, which
`ApplyEdits` applies. With format options, the lines a change touches are
formatted to match, and missing parents are created along the way:

```go
text := "{\n  // keep this\n  \"a\": 1\n}"
o := jsonc.DefaultFormatOptions()
o.TabSize = 2
edits, err := jsonc.Modify(text, jsonc.Path{"b", "c"}, true, jsonc.ModifyOptions{Format: &o})
out, err := jsonc.ApplyEdits(text, edits)
// {
//   // keep this
//   "a": 1,
//   "b": {
//     "c": true
//   }
// }

edits, err = jsonc.Remove(`{"a": 1, "b": [1, 2, 3]}`, jsonc.Path{"b", 1}, jsonc.ModifyOptions{})
// {"a": 1, "b": [1, 3]}
```

Values go through `encoding/json`, so anything with a marshaler works, and
`nil` is JSON `null`; deleting is `Remove`'s job.

`Format` reformats a document, or just a range of it, the same way. It only
touches whitespace between tokens, and an already formatted document gets no
edits:

```go
text := `{"a":1,"b":[true,null]}`
out, _ := jsonc.ApplyEdits(text, jsonc.Format(text, nil, jsonc.FormatOptions{TabSize: 2, InsertSpaces: true}))
```

`StripComments` and `ReplaceComments` are there for when you just want plain
JSON for `encoding/json`.

## Completion

`GetLocation` says where an offset sits in a document, which is what an editor
needs for completion in a half-typed file:

```go
loc := jsonc.GetLocation(`{"a": {"b": 1}}`, 12)
fmt.Println(loc.Path, loc.Matches(jsonc.Path{"a", "*"})) // [a b] true
```

## Differences from node-jsonc-parser

If you're porting JavaScript that uses node-jsonc-parser, these are the things
that'll catch you. Most are Go being Go; the rest are upstream bugs that
weren't worth copying.

- **Offsets and columns are bytes**, not UTF-16 code units. The same for ASCII;
  convert at the boundary if you need LSP positions.
- **Deleting is `Remove`**, not `modify(..., undefined)`; Go's `nil` already
  means `null`.
- **A node's scalar value is `Node.Literal`**, and `Node.Value()` builds the
  whole subtree (upstream's `getNodeValue`).
- **`Parse` doesn't keep key order**, since a `map` can't. `ParseTree` does.
- **Numbers are `float64`** by default, like JavaScript; `UseNumber()` keeps
  them exact as `json.Number`.
- **A number out of range is an error**, not `Infinity`: `1e400` parses to `0`
  with `InvalidNumberFormat`. With `UseNumber()` it's kept as is.
- **Options are functions** (`AllowTrailingComma()`), not an options object.
  `FormatOptions` is still a struct.
- **Errors are returned** where upstream throws, and `ApplyEdits` rejects an
  edit outside the text instead of letting `substring` clamp it.
- **`Modify` encodes with `encoding/json`**, with HTML escaping off. Map keys
  come out sorted where `JSON.stringify` keeps insertion order (use a struct if
  order matters), and U+2028/U+2029 come out escaped.
- **`GetInsertionIndex` is clamped** to the valid range; upstream crashes past
  the end.
- **`Scanner.SetPosition` recounts lines**, so a backward jump can't report a
  stale line or a negative column, and a negative position clamps to 0.
- **A property with no value**, like `{"a":}`, ends before the `}` that closes
  it, so `Modify` adds the next member inside the object; upstream's lands
  after it.
- **`Format` with `KeepLines`** gives a document's leading line breaks as one
  edit, and leaves a document with no tokens alone; upstream adds the breaks
  again on every pass.
- **Array edits don't corrupt.** Upstream turns `[1,2]` into `[12]` when
  removing the last element, and crashes or inserts `undefined` for an index
  outside the array; those are an error here.
- **`Format` doesn't merge tokens.** Upstream joins an unterminated string with
  whatever's on the next line (`{"a\n:1}` becomes `{"a: 1}`), and formats a
  range that starts inside a multi-line comment as if the comment's tail were
  code.

`Remove` matches upstream in one way that might surprise you: comments inside
the span it removes go with it, including a trailing comment on the line before.

## What it doesn't do

No JSON5 (unquoted keys, single quotes), no schema validation, and no
unmarshaling into structs; `StripComments` plus `encoding/json` covers that.

## Testing

Besides upstream's own tests, ported, the format and edit tests replay
upstream's recorded answers for about 29,000 cases from `testdata/`, so this
can't drift from node-jsonc-parser without a test failing. See
[testdata/README.md](testdata/README.md). Four fuzz targets cover the offset
arithmetic, the format round trip, and modify-then-read-back.

## License

MIT; see [LICENSE](LICENSE).
