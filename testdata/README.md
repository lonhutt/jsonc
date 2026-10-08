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

The same for `modify()`: 13068 cases over documents x paths x values x
options, plus where a new key goes. A case upstream throws on records
`"error": true`, and Modify has to fail there too. `TestModifyGoldenCorpus`
also reads each written value back.

Two documents are skipped by name, `{"a":}` and `{"a" 1}`; upstream's tree
ends a property with no value after the `}` that closes it, so its Modify
writes outside the object. Ours ends before the `}`.

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
