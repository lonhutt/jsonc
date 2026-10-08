// gen_format_golden.js regenerates testdata/format-golden.jsonl: the exact
// output of the real node-jsonc-parser's format() for every case in the
// matrix below. The Go test TestFormatGoldenCorpus replays that file, so our
// Format cannot drift from upstream's answers without failing `go test` -- on
// a machine with no node installed.
//
// Regenerating (only needed when pinning a new upstream version):
//
//   cd "$(mktemp -d)"
//   npm pack jsonc-parser@<version>
//   tar xzf jsonc-parser-<version>.tgz
//   node /path/to/repo/testdata/gen_format_golden.js ./package/lib/umd/main.js \
//       > /path/to/repo/testdata/format-golden.jsonl
//
// Then run `go test ./...` and read the diff before keeping it: every changed
// line is a behavior change, either upstream's or ours.
//
// Note on offsets: upstream's are UTF-16 code units, ours are bytes. So for a
// non-ASCII document only the whole-document ("nil" range) case is comparable,
// and "edits" is recorded only for pure-ASCII documents. A Range{2,5} means a
// different span in the two implementations, which is a difference in the
// INPUT, not a difference in behavior -- recording those would freeze a
// mismatch. Multi-byte range formatting is covered by the Go-side property
// tests instead.

'use strict';

const UPSTREAM_VERSION = '3.3.1';

const documents = [
	// Well-formed, compact.
	'{"a":1}', '{}', '[]', '{"a":{"b":1}}', '[1,2]', '[[1]]', '{"a":{"b":{}}}',
	'[[[]]]', '{"a":{"b":{"c":1}}}', '[{"a":1},{"b":2}]', '[1,2,3]',
	'{"a":[1,{"b":null},true,false,"s"]}', '{"a":"x","b":"y"}', '[1,2,3,4,5,6,7,8,9,10]',
	// Whitespace the formatter has to collapse.
	'{\n\n\n  "a"   :    1   }', '{\n\n  "a":1\n}', '{\t"a"\t:\t1\t}',
	'\n\n  {}', '{"a":1}\n\n  ', '{"a":1}\n', '   ', '',
	'{\n}', '[\n]', '  "x"  ', '1', '{"a"    :1}',
	// Already formatted: these must produce no edits at all.
	'{\n  "a": 1\n}', '{\r\n  "a": 1\r\n}', '{\r\n  "a": [\r\n    1\r\n  ]\r\n}',
	'{\n  "a":1,\n  "b":2\n}', '{\n  "a":1 // c\n  ,"b":2\n}',
	// Comments in every syntactic position.
	'{"a":1/* c */}', '{"a":1   /* c */}', '{// c\n"a":1}', '{\n// c\n"a":1}',
	'{"a":1 // c\n}', '{"a":1,// c\n"b":2}', '{"a": // c\n1}', '{"a" // c\n: 1}',
	'[1 // c\n, 2]', '{"a":1\n// c\n}', '{"a":1\n/* c */}', '{\n/* c */ "a":1}',
	'{\n/* c */\n"a":1}', '{ /* c */ }', '// c', '// c\n', '/* c */',
	'{"a":[1,// one\n2],"b":{/*x*/"c":true}}', '[1,\n// c\n]', '{"a":/* x\ny */1}',
	'{"a":{"b":1 // c\n}}', '{// a\n// b\n"x":1}', '[/*a*//*b*/]', '{"a"://c\n//d\n1}',
	'{"a":1\n// c\n}\n', '[\n  // head\n  {"a":1}\n]',
	// Line endings.
	'{\r\n"a":1}', '{\r"a":1}', '{\r\n// c\r\n"a":1\r\n}', '{"a":1 // c\r\n}',
	// Malformed: every path that sets upstream's hasError.
	'[1 2]', '[true false]', '{"a":@}', '[@ 1]', '{"a":@ 1}', '[@,1]',
	'{"a":1/* x', '{"a":"x}', '{"a" "b"}', '}', ']]]', '{"a":1}}', '[1]]',
	'[1,,2]', '{"a":}', '{:1}', '{"a":tru}',
	// Strings that look like syntax.
	'{"a": "line\\nbreak\\tin\\\\string"}', '{"a":"//not a comment"}',
	'{"a":"/* not a comment */"}',
	// Multi-byte.
	'{"\u00e9\u4e2d":1}', '{"a":"\ud83d\ude00"}', '{"a":"\u00fc","b":"\u6f22\u5b57"}',
	// Documents whose ranges are interesting.
	'{\n  "a": {"b":1}\n}', '{\n\t"a": {"b":1}\n}', '{"x":1,\n  "b"  :  2,\n  "c":3}',
	'{"a":1}  \nTAIL', 'x\n{"a":1}\n  junk', '{\n   "a":1\n}', '{\n     "a":1\n}',
	'     {"a":1}',
	// Blank lines, leading and trailing breaks, and one-line layouts: what
	// KeepLines and InsertFinalNewline act on.
	'{\n"a": 1,\n\n"b": 2\n}', '{\n"a": 1,\n\n\n\n"b": 2\n}', '{\r\n\r\n"a":1\r\n}',
	'{\n  \n"a":1}', '\n\n{"a":1}', '\r\n\r\n  {"a":1}', '{"a":1}\n\n\n', '{"a":1}  \n\n',
	'{"a":1, "b":2}', '{\n    "a": 1, "b": 2\n}', '[\n1, 2,\n\n3\n]', '{"a":\n\n1}',
	'{"a"\n:1}', '[1\n,2]', '{\n\n// c\n\n"a":1}', '{"a":1 // c\n\n}',
	'{"a":{\n"b":1}}', '{\n\n}', '[\n\n]', '\n\n', '  \n\n  ', '{\r"a":1\r\r}',
	'{"a":1}\r\n', '[1]\n\r\n',
];

// Named so a failing case reads as something other than a number.
const optionSets = [
	['spaces2', { TabSize: 2, InsertSpaces: true, EOL: '\n' }],
	['tabs4', { TabSize: 4, InsertSpaces: false, EOL: '\n' }],
	['detect', { TabSize: 2, InsertSpaces: true, EOL: '' }],
	['zero', { TabSize: 0, InsertSpaces: false, EOL: '' }],
	['spaces8', { TabSize: 8, InsertSpaces: true, EOL: '\n' }],
	['crlfOpt', { TabSize: 2, InsertSpaces: true, EOL: '\r\n' }],
	['defaults', { TabSize: 4, InsertSpaces: true, EOL: '\n' }],
	['keepLines', { TabSize: 2, InsertSpaces: true, EOL: '\n', KeepLines: true }],
	['keepTabs', { TabSize: 4, InsertSpaces: false, EOL: '', KeepLines: true }],
	['finalNL', { TabSize: 2, InsertSpaces: true, EOL: '\n', InsertFinalNewline: true }],
	['keepFinal', { TabSize: 2, InsertSpaces: true, EOL: '\r\n', KeepLines: true, InsertFinalNewline: true }],
];

// Range shapes: whole document, a prefix, a mid-document span, an empty range,
// one running past the end, and one starting inside a line's indentation.
const rangeShapes = [
	['nil', null],
	['head', { Offset: 0, Length: 3 }],
	['mid', { Offset: 2, Length: 5 }],
	['empty', { Offset: 1, Length: 0 }],
	['past-end', { Offset: 3, Length: 100 }],
	['indent', { Offset: 4, Length: 9 }],
];

function main() {
	// A path argument is resolved against the working directory, not this file.
	const arg = process.argv[2] || 'jsonc-parser';
	const j = require(/^[./]/.test(arg) ? require('path').resolve(arg) : arg);
	const isASCII = (s) => !/[^\x00-\x7f]/.test(s);

	const cases = [];
	for (const text of documents) {
		for (const [optName, o] of optionSets) {
			for (const [rangeName, r] of rangeShapes) {
				if (r && r.Offset > text.length) continue;
				if (r && !isASCII(text)) continue;
				const edits = j.format(
					text,
					r ? { offset: r.Offset, length: r.Length } : undefined,
					{
						tabSize: o.TabSize,
						insertSpaces: o.InsertSpaces,
						eol: o.EOL,
						keepLines: !!o.KeepLines,
						insertFinalNewline: !!o.InsertFinalNewline,
					}
				);
				const c = {
					opts: optName,
					range: rangeName,
					text,
					applied: j.applyEdits(text, edits),
				};
				if (isASCII(text)) {
					c.edits = edits.map((e) => ({ Offset: e.offset, Length: e.length, Content: e.content }));
				}
				cases.push(c);
			}
		}
	}

	// Header line first, so the Go side can pin the upstream version and
	// notice a truncated file.
	process.stdout.write(JSON.stringify({
		upstream: UPSTREAM_VERSION,
		generator: 'testdata/gen_format_golden.js',
		cases: cases.length,
	}) + '\n');
	for (const c of cases) {
		process.stdout.write(JSON.stringify(c) + '\n');
	}
}

main();
