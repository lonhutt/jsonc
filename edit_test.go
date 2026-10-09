package jsonc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestApplyEdits(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		edits []Edit
		want  string
	}{
		{"no edits", "abc", nil, "abc"},
		{"replace", "abc", []Edit{{Offset: 1, Length: 1, Content: "X"}}, "aXc"},
		{"insert", "abc", []Edit{{Offset: 1, Length: 0, Content: "X"}}, "aXbc"},
		{"delete", "abc", []Edit{{Offset: 1, Length: 1, Content: ""}}, "ac"},
		{"append at end", "abc", []Edit{{Offset: 3, Length: 0, Content: "d"}}, "abcd"},
		{"empty text", "", []Edit{{Offset: 0, Length: 0, Content: "x"}}, "x"},
		{
			"multiple ascending",
			"abcdef",
			[]Edit{{Offset: 1, Length: 1, Content: "X"}, {Offset: 4, Length: 1, Content: "Y"}},
			"aXcdYf",
		},
		{
			"multiple descending input order",
			"abcdef",
			[]Edit{{Offset: 4, Length: 1, Content: "Y"}, {Offset: 1, Length: 1, Content: "X"}},
			"aXcdYf",
		},
		{
			"adjacent edits do not overlap",
			"abcd",
			[]Edit{{Offset: 0, Length: 2, Content: "X"}, {Offset: 2, Length: 2, Content: "Y"}},
			"XY",
		},
		// ties at one offset, resolved as upstream does
		{
			"insertions at one offset keep input order",
			"abc",
			[]Edit{{Offset: 1, Length: 0, Content: "I1"}, {Offset: 1, Length: 0, Content: "I2"}},
			"aI1I2bc",
		},
		{
			"insertion goes before a replacement at its offset",
			"abc",
			[]Edit{{Offset: 1, Length: 1, Content: "X"}, {Offset: 1, Length: 0, Content: "I"}},
			"aIXc",
		},
		{
			"insertion at the end of a replacement",
			"abc",
			[]Edit{{Offset: 2, Length: 0, Content: "I"}, {Offset: 1, Length: 1, Content: "X"}},
			"aXIc",
		},
	}
	for _, tc := range tests {
		got, err := ApplyEdits(tc.text, tc.edits)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Two ties can't catch an unstable sort; Go insertion-sorts short slices,
// which is stable anyway.
func TestApplyEditsManyInsertionsKeepOrder(t *testing.T) {
	var edits []Edit
	var want strings.Builder
	for i := range 100 {
		edits = append(edits, Edit{Offset: 1, Length: 0, Content: strconv.Itoa(i) + ","})
		want.WriteString(strconv.Itoa(i) + ",")
		// no-op edits at two other offsets, so the sort has real work around the
		// ties
		edits = append(edits, Edit{Offset: 2 + (99-i)%2, Length: 0, Content: ""})
	}
	got, err := ApplyEdits("abcd", edits)
	if err != nil {
		t.Fatal(err)
	}
	if w := "a" + want.String() + "bcd"; got != w {
		t.Errorf("insertions at one offset were reordered:\n got %q\nwant %q", got, w)
	}
}

func TestApplyEditsRejectsOverlap(t *testing.T) {
	for _, edits := range [][]Edit{
		{{Offset: 0, Length: 3, Content: "X"}, {Offset: 2, Length: 2, Content: "Y"}},
		{{Offset: 1, Length: 1, Content: "X"}, {Offset: 1, Length: 2, Content: "Y"}},
		// An insertion strictly inside a replacement.
		{{Offset: 0, Length: 2, Content: "X"}, {Offset: 1, Length: 0, Content: "I"}},
	} {
		_, err := ApplyEdits("abcdef", edits)
		if err == nil {
			t.Errorf("%+v: overlapping edits must return an error, not last-write-wins", edits)
			continue
		}
		if !strings.Contains(err.Error(), "overlap") {
			t.Errorf("error %q should mention overlap", err)
		}
	}
}

func TestApplyEditsRejectsOutOfBounds(t *testing.T) {
	for _, e := range []Edit{
		{Offset: -1, Length: 0, Content: "x"},
		{Offset: 0, Length: -1, Content: "x"},
		{Offset: 10, Length: 0, Content: "x"},
		{Offset: 2, Length: 5, Content: "x"},
		{Offset: 1, Length: int(^uint(0) >> 1), Content: "x"},
	} {
		if _, err := ApplyEdits("abc", []Edit{e}); err == nil {
			t.Errorf("edit %+v should be rejected", e)
		}
	}
}

func TestApplyEditsDoesNotMutateInput(t *testing.T) {
	edits := []Edit{{Offset: 4, Length: 1, Content: "Y"}, {Offset: 1, Length: 1, Content: "X"}}
	before := append([]Edit(nil), edits...)
	if _, err := ApplyEdits("abcdef", edits); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(edits, before) {
		t.Error("ApplyEdits reordered the caller's slice in place")
	}
}

func TestFormatThenApplyRoundTrip(t *testing.T) {
	in := `{"a":[1,{"b":true}],"c":null}`
	out, err := ApplyEdits(in, Format(in, nil, spaces2()))
	if err != nil {
		t.Fatal(err)
	}
	got, errs := Parse(out)
	if len(errs) != 0 {
		t.Fatalf("formatted output does not parse: %v", errs)
	}
	want, _ := Parse(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("formatting changed the value:\n got %#v\nwant %#v", got, want)
	}
}

// assertModify applies Modify's edits and checks the text; upstream's
// assertEdit.
func assertModify(t *testing.T, in string, path Path, value any, o ModifyOptions, want string) {
	t.Helper()
	edits, err := Modify(in, path, value, o)
	if err != nil {
		t.Fatalf("Modify(%q, %v): %v", in, path, err)
	}
	got, err := ApplyEdits(in, edits)
	if err != nil {
		t.Fatalf("Modify(%q, %v): applying %+v: %v", in, path, edits, err)
	}
	if got != want {
		t.Errorf("Modify(%q, %v, %v):\n got %q\nwant %q", in, path, value, got, want)
	}
}

func modifyOpts() ModifyOptions {
	o := spaces2()
	return ModifyOptions{Format: &o}
}

// Port of upstream's "set property".
func TestModifySetProperty(t *testing.T) {
	o := modifyOpts()
	assertModify(t, "{\n  \"x\": \"y\"\n}", Path{"x"}, "bar", o, "{\n  \"x\": \"bar\"\n}")
	assertModify(t, "true", Path{}, "bar", o, `"bar"`)
	assertModify(t, "{\n  \"x\": \"y\"\n}", Path{"x"}, map[string]any{"key": true}, o, "{\n  \"x\": {\n    \"key\": true\n  }\n}")
	assertModify(t, "{\n  \"a\": \"b\",  \"x\": \"y\"\n}", Path{"a"}, nil, o, "{\n  \"a\": null,  \"x\": \"y\"\n}")
}

// Port of upstream's "insert property".
func TestModifyInsertProperty(t *testing.T) {
	o := modifyOpts()
	assertModify(t, "{}", Path{"foo"}, "bar", o, "{\n  \"foo\": \"bar\"\n}")
	assertModify(t, "{}", Path{"foo", "foo2"}, "bar", o, "{\n  \"foo\": {\n    \"foo2\": \"bar\"\n  }\n}")
	assertModify(t, "{\n}", Path{"foo"}, "bar", o, "{\n  \"foo\": \"bar\"\n}")
	assertModify(t, "  {\n  }", Path{"foo"}, "bar", o, "  {\n    \"foo\": \"bar\"\n  }")
	assertModify(t, "{\n  \"x\": \"y\"\n}", Path{"foo"}, "bar", o, "{\n  \"x\": \"y\",\n  \"foo\": \"bar\"\n}")
	assertModify(t, "{\n  \"x\": \"y\"\n}", Path{"e"}, "null", o, "{\n  \"x\": \"y\",\n  \"e\": \"null\"\n}")
	assertModify(t, "{\n  \"x\": \"y\"\n}", Path{"x"}, "bar", o, "{\n  \"x\": \"bar\"\n}")

	in := "{\n  \"x\": {\n    \"a\": 1,\n    \"b\": true\n  }\n}\n"
	assertModify(t, in, Path{"x"}, "bar", o, "{\n  \"x\": \"bar\"\n}\n")
	assertModify(t, in, Path{"x", "b"}, "bar", o, "{\n  \"x\": {\n    \"a\": 1,\n    \"b\": \"bar\"\n  }\n}\n")
	for index, want := range []string{
		"{\n  \"x\": {\n    \"c\": \"bar\",\n    \"a\": 1,\n    \"b\": true\n  }\n}\n",
		"{\n  \"x\": {\n    \"a\": 1,\n    \"c\": \"bar\",\n    \"b\": true\n  }\n}\n",
		"{\n  \"x\": {\n    \"a\": 1,\n    \"b\": true,\n    \"c\": \"bar\"\n  }\n}\n",
	} {
		oi := modifyOpts()
		oi.GetInsertionIndex = func([]string) int { return index }
		assertModify(t, in, Path{"x", "c"}, "bar", oi, want)
	}
	assertModify(t, in, Path{"c"}, "bar", o, "{\n  \"x\": {\n    \"a\": 1,\n    \"b\": true\n  },\n  \"c\": \"bar\"\n}\n")

	assertModify(t, "{\n  \"a\": [\n    {\n    } \n  ]  \n}", Path{"foo"}, "bar", o, "{\n  \"a\": [\n    {\n    } \n  ],\n  \"foo\": \"bar\"\n}")
	assertModify(t, "", Path{"foo", 0}, "bar", o, "{\n  \"foo\": [\n    \"bar\"\n  ]\n}")
	assertModify(t, "//comment", Path{"foo", 0}, "bar", o, "{\n  \"foo\": [\n    \"bar\"\n  ]\n} //comment")
}

func TestModifyWithoutFormat(t *testing.T) {
	assertModify(t, `{"a":1}`, Path{"b"}, map[string]any{"c": []any{1, 2}}, ModifyOptions{}, `{"a":1,"b": {"c":[1,2]}}`)
}

// KeepLines is forced off; otherwise `{}` would come back as `{ "a": 1 }`.
func TestModifyIgnoresKeepLines(t *testing.T) {
	o := modifyOpts()
	o.Format.KeepLines = true
	assertModify(t, "{}", Path{"a"}, 1, o, "{\n  \"a\": 1\n}")
}

// assertRemove applies Remove's edits and checks the text.
func assertRemove(t *testing.T, in string, path Path, o ModifyOptions, want string) {
	t.Helper()
	edits, err := Remove(in, path, o)
	if err != nil {
		t.Fatalf("Remove(%q, %v): %v", in, path, err)
	}
	got, err := ApplyEdits(in, edits)
	if err != nil {
		t.Fatalf("Remove(%q, %v): applying %+v: %v", in, path, edits, err)
	}
	if got != want {
		t.Errorf("Remove(%q, %v):\n got %q\nwant %q", in, path, got, want)
	}
}

// Port of upstream's "remove property".
func TestRemoveProperty(t *testing.T) {
	o := modifyOpts()
	assertRemove(t, "{\n  \"x\": \"y\"\n}", Path{"x"}, o, "{\n}")
	assertRemove(t, "{\n  \"x\": \"y\", \"a\": []\n}", Path{"x"}, o, "{\n  \"a\": []\n}")
	assertRemove(t, "{\n  \"x\": \"y\", \"a\": []\n}", Path{"a"}, o, "{\n  \"x\": \"y\"\n}")
}

// Ports of upstream's "set item" and array insertion tests.
func TestModifyArray(t *testing.T) {
	o := modifyOpts()
	in := "{\n  \"x\": [1, 2, 3],\n  \"y\": 0\n}"
	assertModify(t, in, Path{"x", 0}, 6, o, "{\n  \"x\": [6, 2, 3],\n  \"y\": 0\n}")
	assertModify(t, in, Path{"x", 1}, 5, o, "{\n  \"x\": [1, 5, 3],\n  \"y\": 0\n}")
	assertModify(t, in, Path{"x", 2}, 4, o, "{\n  \"x\": [1, 2, 4],\n  \"y\": 0\n}")
	assertModify(t, in, Path{"x", 3}, 3, o, "{\n  \"x\": [\n    1,\n    2,\n    3,\n    3\n  ],\n  \"y\": 0\n}")

	insert := modifyOpts()
	insert.IsArrayInsertion = true
	assertModify(t, "[\n  2,\n  3\n]", Path{0}, 1, insert, "[\n  1,\n  2,\n  3\n]")
	assertModify(t, "[\n  1,\n  3\n]", Path{1}, 2, insert, "[\n  1,\n  2,\n  3\n]")
	assertModify(t, "[\n]", Path{0}, 1, o, "[\n  1\n]")
	assertModify(t, "[\n]", Path{1}, 1, o, "[\n  1\n]")
	assertModify(t, "[\n  1,\n  2\n]", Path{2}, 3, o, "[\n  1,\n  2,\n  3\n]")
	assertModify(t, "[\n]", Path{-1}, "bar", o, "[\n  \"bar\"\n]")
	assertModify(t, "[\n  1,\n  2\n]", Path{-1}, "bar", o, "[\n  1,\n  2,\n  \"bar\"\n]")
}

// Ports of upstream's array removal tests.
func TestRemoveArrayElement(t *testing.T) {
	o := modifyOpts()
	assertRemove(t, "[\n  1\n]", Path{0}, o, "[]")
	assertRemove(t, "[\n  1,\n  2,\n  3\n]", Path{1}, o, "[\n  1,\n  3\n]")
	assertRemove(t, "[\n  1,\n  2,\n  \"bar\"\n]", Path{2}, o, "[\n  1,\n  2\n]")
	assertRemove(t, "[\n  1,\n  \"foo\",\n  \"bar\",\n]", Path{2}, o, "[\n  1,\n  \"foo\"\n]")
	assertRemove(t, "// This is a comment\n[\n  1,\n  \"foo\",\n  \"bar\"\n]", Path{2}, o, "// This is a comment\n[\n  1,\n  \"foo\"\n]")
}

// Port of upstream's "set property without formatting".
func TestModifyArrayElementFormatting(t *testing.T) {
	in := "{\n  \"x\": [1, 2, 3],\n  \"y\": 0\n}"
	value := map[string]any{"a": 1, "b": 2}
	assertModify(t, in, Path{"x", 0}, value, modifyOpts(), "{\n  \"x\": [{\n      \"a\": 1,\n      \"b\": 2\n    }, 2, 3],\n  \"y\": 0\n}")
	assertModify(t, in, Path{"x", 0}, value, ModifyOptions{}, "{\n  \"x\": [{\"a\":1,\"b\":2}, 2, 3],\n  \"y\": 0\n}")
}

// Upstream keeps the last byte here and gives `[12]`.
func TestRemoveLastOfOneLineArray(t *testing.T) {
	assertRemove(t, "[1,2]", Path{1}, ModifyOptions{}, "[1]")
	assertRemove(t, "[1, 2, 3]", Path{2}, ModifyOptions{}, "[1, 2]")
	assertRemove(t, "[1, 2 ]", Path{1}, ModifyOptions{}, "[1 ]")
}

// The span removed runs from the previous member, comments and all.
func TestRemoveDeletesCommentsInSpan(t *testing.T) {
	assertRemove(t, "{\n  \"a\": 1, // trailing a\n  // about b\n  \"b\": 2\n}", Path{"b"}, modifyOpts(), "{\n  \"a\": 1\n}")
	assertRemove(t, "[1 /*c*/]", Path{0}, ModifyOptions{}, "[]")
}

func TestRemoveMissingPropertyIsNoEdit(t *testing.T) {
	edits, err := Remove(`{"a": 1}`, Path{"b"}, modifyOpts())
	if err != nil || len(edits) != 0 {
		t.Errorf("got %+v, %v; want no edits", edits, err)
	}
}

func TestRemoveErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		path Path
	}{
		"empty path":       {`{"a": 1}`, Path{}},
		"empty document":   {``, Path{"a"}},
		"missing parent":   {`{"a": 1}`, Path{"x", "y"}},
		"index past end":   {`[1, 2]`, Path{5}},
		"index past end 1": {`[1]`, Path{5}},
		"index -1":         {`[1, 2]`, Path{-1}},
		"index -1 empty":   {`[]`, Path{-1}},
		"negative index":   {`[1, 2]`, Path{-2}},
		"key on an array":  {`[1]`, Path{"a"}},
		"float element":    {`[1]`, Path{0.5}},
	} {
		if edits, err := Remove(tc.in, tc.path, modifyOpts()); err == nil {
			t.Errorf("%s: want an error, got %+v", name, edits)
		}
	}
}

func TestModifyNegativeIndex(t *testing.T) {
	for _, in := range []string{`[1, 2]`, `[]`} {
		o := modifyOpts()
		o.IsArrayInsertion = true
		if edits, err := Modify(in, Path{-2}, 1, o); err == nil {
			t.Errorf("Modify(%q, -2): want an error, got %+v", in, edits)
		}
	}
}

// A property with no value ends before the '}', so the new one lands inside the
// object; upstream's lands after it.
func TestModifyDanglingProperty(t *testing.T) {
	assertModify(t, `{"a":}`, Path{"b"}, 1, ModifyOptions{}, `{"a":,"b": 1}`)
	assertModify(t, `{"a" 1}`, Path{"b"}, 1, ModifyOptions{}, `{"a" 1,"b": 1}`)
}

// The inserted line starts inside the comment; upstream formats `"*/{` as a
// string and puts a line break inside the new key. Read as a comment, it gets
// one space after it, same as upstream gives `/* x */{}`.
func TestModifyAfterAMultiLineComment(t *testing.T) {
	assertModify(t, "/*\n\"*/{}", Path{"}"}, 42, modifyOpts(), "/*\n\"*/ {\n  \"}\": 42\n}")
}

func TestModifyNilIsNull(t *testing.T) {
	assertModify(t, `{"a": 1}`, Path{"a"}, nil, ModifyOptions{}, `{"a": null}`)
}

// encoding/json escapes these by default; JSON.stringify doesn't.
func TestModifyDoesNotEscapeHTML(t *testing.T) {
	assertModify(t, `{"a": 1}`, Path{"a"}, "<&>", ModifyOptions{}, `{"a": "<&>"}`)
}

func TestModifyClampsInsertionIndex(t *testing.T) {
	for _, index := range []int{-5, 99} {
		o := modifyOpts()
		o.GetInsertionIndex = func([]string) int { return index }
		want := "{\n  \"c\": 3,\n  \"a\": 1\n}"
		if index > 0 {
			want = "{\n  \"a\": 1,\n  \"c\": 3\n}"
		}
		assertModify(t, "{\n  \"a\": 1\n}", Path{"c"}, 3, o, want)
	}
}

func TestModifyDoesNotMutatePath(t *testing.T) {
	path := Path{"new", "deep"}
	if _, err := Modify(`{}`, path, 1, modifyOpts()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(path, Path{"new", "deep"}) {
		t.Errorf("path changed to %v", path)
	}
}

func TestModifyErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		in    string
		path  Path
		value any
	}{
		"unencodable value":     {`{}`, Path{"a"}, make(chan int)},
		"float path element":    {`{}`, Path{1.5}, 1},
		"nil path element":      {`{}`, Path{nil}, 1},
		"property on a number":  {`{"a": 1}`, Path{"a", "b"}, 1},
		"property on an array":  {`[1]`, Path{"a"}, 1},
		"index on an object":    {`{"a": 1}`, Path{0}, 1},
		"property on a literal": {`"s"`, Path{"a"}, 1},
	} {
		if _, err := Modify(tc.in, tc.path, tc.value, modifyOpts()); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// modifyGoldenCase is one line of testdata/modify-golden.jsonl.
type modifyGoldenCase struct {
	Opts    string          `json:"opts"`
	Index   string          `json:"index"`
	Text    string          `json:"text"`
	Path    []any           `json:"path"`
	Value   json.RawMessage `json:"value"`
	Remove  bool            `json:"remove"`
	Insert  bool            `json:"arrayInsertion"`
	Applied string          `json:"applied"`
	Edits   []Edit          `json:"edits"`
	Error   bool            `json:"error"`
}

// These match the tables in testdata/gen_modify_golden.js by name.
var modifyGoldenOptions = map[string]*FormatOptions{
	"raw":       nil,
	"spaces2":   {TabSize: 2, InsertSpaces: true, EOL: "\n"},
	"tabs":      {TabSize: 4, InsertSpaces: false, EOL: "\n"},
	"detect":    {TabSize: 2, InsertSpaces: true, EOL: ""},
	"keepLines": {TabSize: 2, InsertSpaces: true, EOL: "\n", KeepLines: true},
	"finalNL":   {TabSize: 2, InsertSpaces: true, EOL: "\n", InsertFinalNewline: true},
}

var modifyGoldenIndexes = map[string]func(key string) func([]string) int{
	"append": func(string) func([]string) int { return nil },
	"front":  func(string) func([]string) int { return func([]string) int { return 0 } },
	"second": func(string) func([]string) int { return func(p []string) int { return min(1, len(p)) } },
	"alpha": func(key string) func([]string) int {
		return func(p []string) int {
			n := 0
			for _, name := range p {
				if name < key {
					n++
				}
			}
			return n
		}
	},
}

// Upstream's tree ends a property with no value after the '}' that closes it,
// so its Modify writes outside the object (`{"a":},"b": 1`). Ours ends before
// the '}'; see TestModifyDanglingProperty.
var modifyGoldenTreeDivergence = map[string]bool{`{"a":}`: true, `{"a" 1}`: true}

// arrayDivergence names the upstream array bug a case hits, if any; we return
// an error for the first two.
//   - "range": Remove with an index outside the array, which upstream crashes
//     on, answers by emptying the array, or turns into an inserted "undefined".
//   - "negative": Modify with an index below -1, which upstream crashes on
//     unless the array is empty and IsArrayInsertion is set, or doesn't exist
//     yet.
//   - "merge": Remove of the last element of a one-line array, where upstream
//     keeps its last byte (`[1,2]` -> `[12]`).
func arrayDivergence(text string, path Path, remove bool) string {
	for _, seg := range path {
		if i, ok := seg.(int); ok && i < -1 && !remove {
			return "negative"
		}
	}
	if len(path) == 0 {
		return ""
	}
	index, ok := path[len(path)-1].(int)
	if !ok {
		return ""
	}
	root, _ := ParseTree(text)
	parent := FindNodeAtLocation(root, path[:len(path)-1])
	if parent == nil || parent.Type != NodeArray {
		return ""
	}
	n := len(parent.Children)
	if !remove {
		if index < -1 {
			return "negative"
		}
		return ""
	}
	if index < 0 || index >= n {
		return "range"
	}
	last := parent.Children[n-1]
	if n > 1 && index == n-1 && parent.Offset+parent.Length-2 < last.Offset+last.Length {
		return "merge"
	}
	return ""
}

// TestModifyGoldenCorpus replays node-jsonc-parser's recorded modify() output
// from testdata/modify-golden.jsonl, the same way TestFormatGoldenCorpus does.
// It also reads each written value back, and checks what's left after each
// removal; both hold for any correct Modify and Remove.
func TestModifyGoldenCorpus(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "modify-golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	if !sc.Scan() {
		t.Fatal("the golden corpus is empty")
	}
	var header struct {
		Upstream string `json:"upstream"`
		Cases    int    `json:"cases"`
	}
	if err := json.Unmarshal(sc.Bytes(), &header); err != nil || header.Cases == 0 {
		t.Fatalf("bad header %q: %v", sc.Bytes(), err)
	}

	failures, n, skipped := 0, 0, 0
	diverged := map[string]int{}
	fail := func(line int, c modifyGoldenCase, format string, args ...any) {
		failures++
		if failures <= 10 {
			t.Errorf("golden line %d (opts %s, index %s) %q %v %s: %s", line, c.Opts, c.Index, c.Text, c.Path, c.Value, fmt.Sprintf(format, args...))
		}
	}
	for line := 2; sc.Scan(); line++ {
		var c modifyGoldenCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("line %d: %v", line, err)
		}
		n++
		if modifyGoldenTreeDivergence[c.Text] {
			skipped++
			continue
		}
		format, ok := modifyGoldenOptions[c.Opts]
		index, ok2 := modifyGoldenIndexes[c.Index]
		if !ok || !ok2 {
			t.Fatalf("line %d: unknown opts %q or index %q", line, c.Opts, c.Index)
		}
		path := make(Path, len(c.Path))
		for i, seg := range c.Path {
			if f, isNum := seg.(float64); isNum {
				seg = int(f)
			}
			path[i] = seg
		}
		var value any
		if !c.Remove {
			value = decodeNumbers(t, c.Value)
		}
		var key string
		if len(path) > 0 {
			key, _ = path[len(path)-1].(string)
		}

		o := ModifyOptions{Format: format, GetInsertionIndex: index(key), IsArrayInsertion: c.Insert}
		var edits []Edit
		if c.Remove {
			edits, err = Remove(c.Text, path, o)
		} else {
			edits, err = Modify(c.Text, path, value, o)
		}
		if reason := arrayDivergence(c.Text, path, c.Remove); reason != "" {
			diverged[reason]++
			if reason != "merge" {
				if err == nil {
					fail(line, c, "want an error, got %+v", edits)
				}
				continue
			}
			got, err := ApplyEdits(c.Text, edits)
			if err != nil {
				fail(line, c, "%+v: %v", edits, err)
			} else if msg := checkRemoved(c.Text, got, path); msg != "" {
				fail(line, c, "%s", msg)
			}
			continue
		}
		if c.Error {
			if err == nil {
				fail(line, c, "upstream throws, we returned %+v", edits)
			}
			continue
		}
		if err != nil {
			fail(line, c, "unexpected error: %v", err)
			continue
		}
		got, err := ApplyEdits(c.Text, edits)
		if err != nil {
			fail(line, c, "applying %+v: %v", edits, err)
			continue
		}
		if got != c.Applied && format != nil && format.InsertFinalNewline {
			off := *format
			off.InsertFinalNewline = false
			o.Format = &off
			var without string
			if e, err := setProperty(c.Text, path, value, c.Remove, o); err == nil {
				without, _ = ApplyEdits(c.Text, e)
			}
			if finalNewlineMidDocument(got, c.Applied, without) {
				diverged["midNewline"]++
				continue
			}
		}
		if got != c.Applied {
			fail(line, c, "\n   got %q\n  want %q", got, c.Applied)
			continue
		}
		if isASCII(c.Text) && !editsEqual(edits, c.Edits) {
			fail(line, c, "\n  got edits  %+v\n  want edits %+v", edits, c.Edits)
			continue
		}
		if c.Remove {
			if msg := checkRemoved(c.Text, got, path); msg != "" {
				fail(line, c, "%s", msg)
			}
		} else if at, ok := landingPath(c.Text, path, c.Insert); ok {
			root, errs := ParseTree(got)
			var want any
			_ = json.Unmarshal(c.Value, &want)
			if n := FindNodeAtLocation(root, at); len(errs) != 0 || n == nil || !reflect.DeepEqual(n.Value(), want) {
				fail(line, c, "reading the value back from %q failed", got)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n != header.Cases {
		t.Errorf("golden corpus is truncated: read %d cases, header says %d", n, header.Cases)
	}
	if failures > 10 {
		t.Errorf("%d golden cases differ from upstream %s (first 10 shown)", failures, header.Upstream)
	}
	if skipped == 0 || diverged["range"] == 0 || diverged["merge"] == 0 || diverged["negative"] == 0 || diverged["midNewline"] == 0 {
		t.Errorf("a divergence no longer shows up in the corpus (skipped %d, %v); drop its handling if so", skipped, diverged)
	}
	t.Logf("replayed %d cases from node-jsonc-parser %s; skipped %d, divergences %v", n-skipped, header.Upstream, skipped, diverged)
}

// landingPath is where Modify puts the value: path, except that an array index
// of -1 or past the end appends. ok is false when text doesn't parse cleanly
// or path ends in an index on an array that doesn't exist yet.
func landingPath(text string, path Path, insert bool) (Path, bool) {
	root, errs := ParseTree(text)
	if len(errs) != 0 || len(path) == 0 {
		return path, len(errs) == 0
	}
	index, isIndex := path[len(path)-1].(int)
	if !isIndex {
		return path, true
	}
	parent := FindNodeAtLocation(root, path[:len(path)-1])
	if parent == nil || parent.Type != NodeArray {
		return nil, false
	}
	n := len(parent.Children)
	if index == -1 || index > n || (index == n && !insert) {
		index = n
	}
	return append(path[:len(path)-1:len(path)-1], index), true
}

// checkRemoved reports what's wrong, if anything, with after as before with
// the value at path removed. Only checked when before parses cleanly.
func checkRemoved(before, after string, path Path) string {
	v, errs := Parse(before)
	if len(errs) != 0 || hasDuplicateKeys(before) {
		return ""
	}
	got, errs := Parse(after)
	if len(errs) != 0 {
		return fmt.Sprintf("result %q doesn't parse: %v", after, errs)
	}
	parent := v
	for _, seg := range path[:len(path)-1] {
		switch c := parent.(type) {
		case map[string]any:
			parent = c[seg.(string)]
		case []any:
			parent = c[seg.(int)]
		}
	}
	switch c := parent.(type) {
	case map[string]any:
		delete(c, path[len(path)-1].(string))
	case []any:
		i := path[len(path)-1].(int)
		removed := append(c[:i:i], c[i+1:]...)
		if len(path) == 1 {
			v = removed
		} else {
			setAt(v, path[:len(path)-1], removed)
		}
	}
	if !reflect.DeepEqual(got, v) {
		return fmt.Sprintf("after removing: got %#v, want %#v", got, v)
	}
	return ""
}

// hasDuplicateKeys reports whether any object in text repeats a key, which
// makes "the value at path" ambiguous.
func hasDuplicateKeys(text string) bool {
	root, _ := ParseTree(text)
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		seen := map[string]bool{}
		for _, c := range n.Children {
			if n.Type == NodeObject && len(c.Children) > 0 {
				k, _ := c.Children[0].Literal.(string)
				if seen[k] {
					return true
				}
				seen[k] = true
			}
			if walk(c) {
				return true
			}
		}
		return false
	}
	return root != nil && walk(root)
}

// setAt replaces the value at path inside v, which holds maps and slices.
func setAt(v any, path Path, value any) {
	for i, seg := range path {
		if i == len(path)-1 {
			switch c := v.(type) {
			case map[string]any:
				c[seg.(string)] = value
			case []any:
				c[seg.(int)] = value
			}
			return
		}
		switch c := v.(type) {
		case map[string]any:
			v = c[seg.(string)]
		case []any:
			v = c[seg.(int)]
		}
	}
}

// decodeNumbers keeps numbers as json.Number, so they marshal back exactly as
// upstream wrote them.
func decodeNumbers(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	return v
}

// Upstream stops 2 bytes before the ']', which can land inside a comment there:
// `[1, 2 /* c */]` becomes `[1/]`.
func TestRemoveLastBeforeAComment(t *testing.T) {
	assertRemove(t, "[1, 2 /* c */]", Path{1}, ModifyOptions{}, "[1/* c */]")
	assertRemove(t, "[1,2/**/]", Path{1}, ModifyOptions{}, "[1/**/]")
	assertRemove(t, `{"a":[1, 2 /*x*/]}`, Path{"a", 1}, ModifyOptions{}, `{"a":[1/*x*/]}`)
}

// An index below -1 is an error whether or not the array exists yet.
func TestModifyNegativeIndexInNewArray(t *testing.T) {
	if edits, err := Modify(`{}`, Path{"a", -5}, 1, modifyOpts()); err == nil {
		t.Errorf("want an error, got %+v", edits)
	}
	assertModify(t, `{}`, Path{"a", -1}, 1, ModifyOptions{}, `{"a": [1]}`)
}
