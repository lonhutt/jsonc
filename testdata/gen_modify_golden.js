// gen_modify_golden.js writes testdata/modify-golden.jsonl: upstream modify()'s
// output for every case below, removals included (value undefined, recorded
// as "remove": true). TestModifyGoldenCorpus replays it, so Modify and Remove
// can't drift from upstream without failing `go test`, and no node is needed.
//
// Regenerate it with format-golden.jsonl; see README.md.

'use strict';

const UPSTREAM_VERSION = '3.3.1';

const documents = [
	// shapes from upstream's own edit tests
	'{}', '{\n}', '  {\n  }', '{\n  "x": "y"\n}', 'true', '',
	'{\n  "x": {\n    "a": 1,\n    "b": true\n  }\n}\n',
	'{\n  "a": [\n    {\n    } \n  ]  \n}', '{\n  "a": "b",  "x": "y"\n}', '//comment',
	// compact and one-line
	'{"a":1}', '{"a":1,"b":2}', '{"x":{"a":1}}', '{ "a": 1, "x": {} }',
	// comments next to what changes
	'{\n  // c\n  "a": 1 // t\n}', '{\n  "a": 1, /* b */\n  "x": 2\n}', '/* head */ {"a":1}',
	// line endings and indentation
	'{\r\n  "a": 1\r\n}', '{\n\t"a": 1\n}', '    {"a":1}',
	// not an object where one is needed
	'[1,2]', '{"a":[1]}', '{"a":"s"}', '1', '  ',
	// malformed
	'{"a":1,}', '{"a":}', '{"a" 1}', '{"a":1', '{"a":1,"a":2}',
	// multi-byte
	'{"é":1}', '{"a":"中文"}',
	// arrays, including upstream's own shapes
	'[1,2]', '[1, 2, 3]', '[\n  1,\n  2\n]', '[\n  2,\n  3\n]', '[\n  1,\n  "foo",\n  "bar",\n]',
	'[]', '[\n]', '[1 /*c*/]', '// head\n[\n  1,\n  "foo",\n  "bar"\n]', '{"a":[1,2]}',
	'{\n  "x": [1, 2, 3],\n  "y": 0\n}', '[[1],[2,3]]', '[1,2,]', '[1 2]',
	// comments next to what's removed
	'{\n  "a": 1, // trailing a\n  // about b\n  "b": 2\n}', '{\n  // about a\n  "a": 1,\n  "b": 2\n}',
	// whitespace Format rewrites around the splice
	'\t true', '\t {"a":1}', '{\n  "a": 1\t\n      }', '{\n  "x": {\n    "a": 1 \n        }\n}',
];

const paths = [
	[], ['a'], ['b'], ['x'], ['x', 'a'], ['x', 'c'], ['a', 'b'],
	['new', 'deep'], ['new', 0], ['new', 0, 'k'], ['é'],
	[0], [1], [2], [5], [-1], [-2], ['a', 0], ['a', 1], ['a', -1], ['x', 0], ['x', 3], [0, 0], [1, 0],
];

// Object keys sorted, so a Go map marshals them in the same order.
const values = [
	'bar', 1, 2.5, null, true, { key: true }, [1, 2], {}, '<&>', '中文',
	{ a: { b: [1, { c: null }] } },
];

const optionSets = [
	['raw', null],
	['spaces2', { TabSize: 2, InsertSpaces: true, EOL: '\n' }],
	['tabs', { TabSize: 4, InsertSpaces: false, EOL: '\n' }],
	['detect', { TabSize: 2, InsertSpaces: true, EOL: '' }],
	['keepLines', { TabSize: 2, InsertSpaces: true, EOL: '\n', KeepLines: true }],
	['finalNL', { TabSize: 2, InsertSpaces: true, EOL: '\n', InsertFinalNewline: true }],
];

// The Go test has the same functions under the same names.
const insertionIndexes = [
	['append', null],
	['front', () => 0],
	['second', (props) => Math.min(1, props.length)],
	['alpha', (props, key) => props.filter((p) => p < key).length],
];

function main() {
	const arg = process.argv[2] || 'jsonc-parser';
	const j = require(/^[./]/.test(arg) ? require('path').resolve(arg) : arg);
	const isASCII = (s) => !/[^\x00-\x7f]/.test(s);

	const cases = [];
	const add = (text, path, value, optName, o, idxName, idx, isArrayInsertion) => {
		const key = path[path.length - 1];
		const options = {};
		if (o) {
			options.formattingOptions = {
				tabSize: o.TabSize,
				insertSpaces: o.InsertSpaces,
				eol: o.EOL,
				keepLines: !!o.KeepLines,
				insertFinalNewline: !!o.InsertFinalNewline,
			};
		}
		if (idx) options.getInsertionIndex = (props) => idx(props, key);
		if (isArrayInsertion) options.isArrayInsertion = true;
		const c = { opts: optName, index: idxName, text, path };
		if (value === undefined) c.remove = true; else c.value = value;
		if (isArrayInsertion) c.arrayInsertion = true;
		try {
			const edits = j.modify(text, path, value, options);
			c.applied = j.applyEdits(text, edits);
			if (isASCII(text)) {
				c.edits = edits.map((e) => ({ Offset: e.offset, Length: e.length, Content: e.content }));
			}
		} catch (e) {
			c.error = true;
		}
		cases.push(c);
	};

	for (const text of documents) {
		for (const path of paths) {
			// every value raw and formatted on a few paths, two values elsewhere;
			// the other option sets with one
			const fullValues = ['[]', '["a"]', '["x","c"]', '["new","deep"]', '[0]', '[-1]'].includes(JSON.stringify(path));
			for (const value of fullValues ? values : ['bar', values[values.length - 1]]) {
				for (const [optName, o] of optionSets.slice(0, 2)) {
					add(text, path, value, optName, o, 'append', null);
				}
			}
			for (const [optName, o] of optionSets.slice(2)) {
				add(text, path, values[values.length - 1], optName, o, 'append', null);
			}
			// insertion order only matters for where a new key goes
			if (typeof path[path.length - 1] === 'string') {
				for (const [idxName, idx] of insertionIndexes.slice(1)) {
					add(text, path, 'bar', 'spaces2', optionSets[1][1], idxName, idx);
				}
			}
			if (typeof path[path.length - 1] === 'number') {
				add(text, path, 'bar', 'spaces2', optionSets[1][1], 'append', null, true);
				add(text, path, 'bar', 'raw', null, 'append', null, true);
			}
			for (const [optName, o] of optionSets.slice(0, 3)) {
				add(text, path, undefined, optName, o, 'append', null);
			}
		}
	}

	process.stdout.write(JSON.stringify({
		upstream: UPSTREAM_VERSION,
		generator: 'testdata/gen_modify_golden.js',
		cases: cases.length,
	}) + '\n');
	for (const c of cases) {
		process.stdout.write(JSON.stringify(c) + '\n');
	}
}

main();
