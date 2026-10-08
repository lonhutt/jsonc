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

### Regenerating

Only when pinning a new upstream version. Bump `UPSTREAM_VERSION` in the
generator first:

```sh
cd "$(mktemp -d)"
npm pack jsonc-parser@<version>
tar xzf jsonc-parser-<version>.tgz
node /path/to/repo/testdata/gen_format_golden.js ./package/lib/umd/main.js \
    > /path/to/repo/testdata/format-golden.jsonl
```

Then run `go test ./...` and read the diff before keeping it; every changed
line is a behavior change, upstream's or ours.
