# testdata

## format-golden.jsonl

[node-jsonc-parser](https://github.com/microsoft/node-jsonc-parser)'s
`format()` output for 7337 cases: 114 documents x 11 option sets x 6 range
shapes. `TestFormatGoldenCorpus` replays it, so `Format` can't drift from
upstream without failing `go test`; the answers are frozen here, so no node is
needed.

The rest of the format tests hold for any sane whitespace rewriter; this file
is what ties us to upstream's.

The first line is a header with the upstream version, the generator and the
case count, so a truncated file fails. Every other line is one case:

- `opts` and `range` name entries in the generator's tables
- `text` is the input
- `applied` is upstream's output
- `edits` is upstream's edit list; ASCII documents only, since upstream's
  offsets are UTF-16 and ours are bytes

## modify-golden.jsonl

The same for `modify()`, Remove included: 22204 cases over documents x paths
x values x options, plus where a new key goes and `isArrayInsertion`. A
removal records `"remove": true` in place of a value (JSON has no
`undefined`), and a case upstream throws on records `"error": true`, so we
have to fail there too. `TestModifyGoldenCorpus` also reads each written
value back and checks what's left after each removal.

Where we differ on purpose, the test says so by name instead of comparing:

- `{"a":}` and `{"a" 1}` are skipped; upstream's tree ends a property with no
  value after the `}` that closes it, so its Modify writes outside the object
- `"range"`, `"negative"`: an array index upstream crashes on (or answers with
  an emptied array, or the text `undefined`); we have to return an error
- `"merge"`: removing the last element of a one-line array, where upstream
  gives `[1,2]` -> `[12]`; we have to leave the right value

## Regenerating

Only when pinning a new upstream version. Bump `UPSTREAM_VERSION` in both
generators first:

```sh
cd "$(mktemp -d)"
npm pack jsonc-parser@<version>
tar xzf jsonc-parser-<version>.tgz
for c in format modify; do
  node /path/to/repo/testdata/gen_${c}_golden.js ./package/lib/umd/main.js \
      > /path/to/repo/testdata/${c}-golden.jsonl
done
```

Then run `go test ./...` and read the diff before keeping it; every changed
line is a behavior change, upstream's or ours.
