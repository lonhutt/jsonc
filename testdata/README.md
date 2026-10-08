# testdata

## format-golden.jsonl

The exact output of the real [node-jsonc-parser](https://github.com/microsoft/node-jsonc-parser)
`format()` for 7337 cases: 114 documents x 11 option sets x 6 range shapes.
`TestFormatGoldenCorpus` replays it, so our `Format` cannot drift from
upstream's answers without failing `go test`. **No node is needed to run the
tests** — the answers are frozen here.

The rest of the format suite pins some strings by hand and otherwise checks
structural properties (whitespace-only edits, idempotence, token preservation)
that hold for *any* sane whitespace rewriter. This file is what ties us to
*upstream's* rewriter specifically.

First line is a header: the upstream version, the generator, and the case count
(so a truncated file fails rather than passing quietly). Every other line is one
case: `opts` and `range` name entries in the tables shared with the generator,
`text` is the input, `applied` is upstream's output, and `edits` is upstream's
edit list — recorded only for ASCII documents, because upstream's offsets are
UTF-16 code units and ours are bytes.

### Regenerating

Only when pinning a new upstream version:

```sh
cd "$(mktemp -d)"
npm pack jsonc-parser@<version>
tar xzf jsonc-parser-<version>.tgz
node /path/to/repo/testdata/gen_format_golden.js ./package/lib/umd/main.js \
    > /path/to/repo/testdata/format-golden.jsonl
```

Bump `UPSTREAM_VERSION` in the generator first. Then run `go test ./...` and
**read the diff before keeping it**: every changed line is a behavior change,
either upstream's or ours.
