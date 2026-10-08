package jsonc

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// collectErrors returns Visit's error codes in order.
func collectErrors(t *testing.T, text string, opts ...ParseOption) []ParseErrorCode {
	t.Helper()
	var codes []ParseErrorCode
	Visit(text, Visitor{
		OnError: func(c ParseErrorCode, _ Span) { codes = append(codes, c) },
	}, opts...)
	return codes
}

// events records the callback sequence as strings, so a failure shows what
// happened.
func events(text string, opts ...ParseOption) []string {
	var out []string
	Visit(text, Visitor{
		OnObjectBegin:    func(Span) bool { out = append(out, "{"); return true },
		OnObjectEnd:      func(Span) { out = append(out, "}") },
		OnArrayBegin:     func(Span) bool { out = append(out, "["); return true },
		OnArrayEnd:       func(Span) { out = append(out, "]") },
		OnObjectProperty: func(n string, _ Span) { out = append(out, "key:"+n) },
		OnLiteralValue:   func(v any, _ Span) { out = append(out, fmt.Sprintf("val:%v", v)) },
		OnSeparator:      func(s string, _ Span) { out = append(out, "sep:"+s) },
		OnComment:        func(Span) { out = append(out, "comment") },
	}, opts...)
	return out
}

func TestVisitSequence(t *testing.T) {
	tests := []struct {
		text string
		want []string
	}{
		{`{}`, []string{"{", "}"}},
		{`[]`, []string{"[", "]"}},
		{`{"a":1}`, []string{"{", "key:a", "sep::", "val:1", "}"}},
		{`[1,2]`, []string{"[", "val:1", "sep:,", "val:2", "]"}},
		{`{"a":{"b":[1]}}`, []string{"{", "key:a", "sep::", "{", "key:b", "sep::", "[", "val:1", "]", "}", "}"}},
		{`1`, []string{"val:1"}},
		{`"s"`, []string{"val:s"}},
		{`// c` + "\n" + `1`, []string{"comment", "val:1"}},
		{`/* c */ 1`, []string{"comment", "val:1"}},
		// all five literal kinds; the string comes back decoded
		{`[null,true,false,1,"s"]`, []string{"[", "val:<nil>", "sep:,", "val:true", "sep:,", "val:false", "sep:,", "val:1", "sep:,", "val:s", "]"}},
		{`["a\tb\u00e9"]`, []string{"[", "val:a\tb\u00e9", "]"}},
	}
	for _, tc := range tests {
		if got := events(tc.text); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("events(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestVisitSpanOffsets(t *testing.T) {
	text := `{"ab":123}`
	var keySpan, valSpan Span
	Visit(text, Visitor{
		OnObjectProperty: func(_ string, s Span) { keySpan = s },
		OnLiteralValue:   func(_ any, s Span) { valSpan = s },
	})
	// the key span covers the quoted "ab"
	if keySpan.Offset != 1 || keySpan.Length != 4 {
		t.Errorf("key span = offset %d len %d, want 1,4", keySpan.Offset, keySpan.Length)
	}
	if got := text[keySpan.Offset : keySpan.Offset+keySpan.Length]; got != `"ab"` {
		t.Errorf("key span text = %q, want %q", got, `"ab"`)
	}
	if got := text[valSpan.Offset : valSpan.Offset+valSpan.Length]; got != "123" {
		t.Errorf("value span text = %q, want %q", got, "123")
	}
}

func TestVisitPathSupplier(t *testing.T) {
	var paths []Path
	Visit(`{"a":[10,{"b":true}]}`, Visitor{
		OnLiteralValue: func(_ any, s Span) { paths = append(paths, s.Path()) },
	})
	want := []Path{
		{"a", 0},
		{"a", 1, "b"},
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("literal paths = %v, want %v", paths, want)
	}
}

func TestVisitErrorRecovery(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []ParseErrorCode
	}{
		{"missing comma", `{"a":1 "b":2}`, []ParseErrorCode{CommaExpected}},
		{"missing colon", `{"a" 1}`, []ParseErrorCode{ColonExpected}},
		{"property name expected", `{1:2}`, []ParseErrorCode{PropertyNameExpected, ValueExpected}},
		{"unclosed object", `{"a":1`, []ParseErrorCode{CloseBraceExpected}},
		{"unclosed array", `[1`, []ParseErrorCode{CloseBracketExpected}},
		{"trailing comma rejected", `[1,]`, []ParseErrorCode{ValueExpected}},
		{"empty content", ``, []ParseErrorCode{ValueExpected}},
		{"trailing content", `1 2`, []ParseErrorCode{EndOfFileExpected}},
	}
	for _, tc := range tests {
		if got := collectErrors(t, tc.text); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: errors for %q = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}

func TestVisitRecoveryContinues(t *testing.T) {
	// a missing comma resyncs at the next property instead of dropping the rest
	got := events(`{"a":1 "b":2}`)
	want := []string{"{", "key:a", "sep::", "val:1", "key:b", "sep::", "val:2", "}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v (parser must recover and see property b)", got, want)
	}
}

func TestVisitSynthesizesClosers(t *testing.T) {
	// an unterminated object still fires OnObjectEnd, so a consumer's stack
	// unwinds
	got := events(`{"a":1`)
	want := []string{"{", "key:a", "sep::", "val:1", "}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v (OnObjectEnd must fire)", got, want)
	}
}

func TestParseOptions(t *testing.T) {
	if got := collectErrors(t, `[1,]`, AllowTrailingComma()); len(got) != 0 {
		t.Errorf("AllowTrailingComma: got errors %v, want none", got)
	}
	if got := collectErrors(t, ``, AllowEmptyContent()); len(got) != 0 {
		t.Errorf("AllowEmptyContent: got errors %v, want none", got)
	}
	if got := collectErrors(t, `// c`+"\n"+`1`, DisallowComments()); !reflect.DeepEqual(got, []ParseErrorCode{InvalidCommentToken}) {
		t.Errorf("DisallowComments: got %v, want [InvalidCommentToken]", got)
	}
}

func TestParseErrorFields(t *testing.T) {
	var errs []ParseError
	Visit("{\n  \"a\" 1\n}", Visitor{
		OnError: func(c ParseErrorCode, s Span) {
			errs = append(errs, ParseError{Code: c, Offset: s.Offset, Length: s.Length, StartLine: s.StartLine, StartCol: s.StartCol})
		},
	})
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	if errs[0].Code != ColonExpected {
		t.Errorf("code = %v, want ColonExpected", errs[0].Code)
	}
	if errs[0].StartLine != 1 {
		t.Errorf("StartLine = %d, want 1", errs[0].StartLine)
	}
	// colon missing before the 1 at offset 8; line 1, column 6
	const wantMsg = `ColonExpected at offset 8 (line 1, column 6)`
	if got := errs[0].Error(); got != wantMsg {
		t.Errorf("Error() = %q, want %q", got, wantMsg)
	}
}

func TestParseErrorCodeString(t *testing.T) {
	if ColonExpected.String() != "ColonExpected" {
		t.Errorf("ColonExpected.String() = %q, want %q", ColonExpected.String(), "ColonExpected")
	}
}

func TestVisitNumberRange(t *testing.T) {
	probe := func(in string, opts ...ParseOption) (any, []ParseErrorCode) {
		var v any
		var codes []ParseErrorCode
		Visit(in, Visitor{
			OnLiteralValue: func(x any, _ Span) { v = x },
			OnError:        func(c ParseErrorCode, _ Span) { codes = append(codes, c) },
		}, opts...)
		return v, codes
	}

	// out of range: reported and zeroed as a float64..
	if v, codes := probe(`1e400`); v != float64(0) || len(codes) != 1 || codes[0] != InvalidNumberFormat {
		t.Errorf("1e400 float64 path = %#v, errs %v; want 0 and [InvalidNumberFormat]", v, codes)
	}
	// ..but kept exactly with UseNumber, no error
	if v, codes := probe(`1e400`, UseNumber()); v != json.Number("1e400") || len(codes) != 0 {
		t.Errorf("1e400 UseNumber = %#v, errs %v; want json.Number(\"1e400\") and no errors", v, codes)
	}
	if v, _ := probe(`12345678901234567890`, UseNumber()); v != json.Number("12345678901234567890") {
		t.Errorf("large int UseNumber = %#v, want digits preserved", v)
	}
	// a syntax error still reports under UseNumber
	if _, codes := probe(`1e`, UseNumber()); len(codes) == 0 {
		t.Error("1e under UseNumber reported no error; a syntax error must still be reported")
	}
}

// "1e" scans as 1 with the bad exponent left out, so it's one error and the
// value 1, as upstream; not 0 with InvalidNumberFormat too.
func TestVisitNumberSyntaxErrorCodes(t *testing.T) {
	for _, opts := range [][]ParseOption{nil, {UseNumber()}} {
		v, errs := Parse(`1e`, opts...)
		if len(errs) != 1 || errs[0].Code != UnexpectedEndOfNumber || (v != float64(1) && v != json.Number("1")) {
			t.Errorf("Parse(1e, %d opts) = %#v, %v", len(opts), v, errs)
		}
	}
}

func TestVisitReturnValue(t *testing.T) {
	cases := []struct {
		text string
		opts []ParseOption
		want bool
	}{
		{``, nil, false},
		{``, []ParseOption{AllowEmptyContent()}, true},
		{`,`, nil, false},
		{`{`, nil, true},
		{`{"a":1}`, nil, true},
	}
	for _, tc := range cases {
		if got := Visit(tc.text, Visitor{}, tc.opts...); got != tc.want {
			t.Errorf("Visit(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestVisitPathIsSnapshotNotLiveRead(t *testing.T) {
	// Call Path() only after Visit returns and the stack has unwound; a live
	// read instead of a snapshot fails here while TestVisitPathSupplier passes.
	var spans []Span
	Visit(`{"a":[10,{"b":true}]}`, Visitor{
		OnLiteralValue: func(_ any, s Span) { spans = append(spans, s) },
	})
	var got []Path
	for _, s := range spans {
		got = append(got, s.Path())
	}
	want := []Path{{"a", 0}, {"a", 1, "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deferred Path() = %v, want %v", got, want)
	}
}

func TestVisitSynthesizesNestedClosers(t *testing.T) {
	got := events(`{"a":{"b":1`)
	want := []string{"{", "key:a", "sep::", "{", "key:b", "sep::", "val:1", "}", "}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v (both closers must fire)", got, want)
	}
}

func BenchmarkVisit(b *testing.B) {
	const text = `{
	// a line comment
	"name": "benchmark",
	"nested": {"flag": true, "count": 42, "ratio": 3.14159, "missing": null},
	"list": [1, 2, 3, "four", {"five": 5}, [6, 7]],
	/* a block comment */
	"escaped": "a\tb\nc\"d\\eéf"
}`
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for i := 0; i < b.N; i++ {
		Visit(text, Visitor{})
	}
}

func TestParseValues(t *testing.T) {
	tests := []struct {
		text string
		want any
	}{
		{`1`, float64(1)},
		{`-1.5e2`, float64(-150)},
		{`"s"`, "s"},
		{`true`, true},
		{`false`, false},
		{`null`, nil},
		{`[]`, []any{}},
		{`{}`, map[string]any{}},
		{`[1,"a",null]`, []any{float64(1), "a", nil}},
		{`{"a":1,"b":[true]}`, map[string]any{"a": float64(1), "b": []any{true}}},
		{`{"a":{"b":{"c":1}}}`, map[string]any{"a": map[string]any{"b": map[string]any{"c": float64(1)}}}},
		{"{\n // c\n \"a\":1\n}", map[string]any{"a": float64(1)}},
	}
	for _, tc := range tests {
		got, errs := Parse(tc.text)
		if len(errs) != 0 {
			t.Errorf("Parse(%q) errors = %v, want none", tc.text, errs)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parse(%q) = %#v, want %#v", tc.text, got, tc.want)
		}
	}
}

func TestParseMatchesEncodingJSON(t *testing.T) {
	// on valid strict JSON, Parse agrees with encoding/json
	for _, text := range []string{`{"a":[1,2,{"b":null}],"c":"x"}`, `[]`, `{}`, `3.14`, `"é"`} {
		var want any
		if err := json.Unmarshal([]byte(text), &want); err != nil {
			t.Fatalf("test input %q is not valid JSON: %v", text, err)
		}
		got, errs := Parse(text)
		if len(errs) != 0 {
			t.Errorf("Parse(%q) errors = %v", text, errs)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %#v, encoding/json = %#v", text, got, want)
		}
	}
}

func TestParseUseNumber(t *testing.T) {
	got, errs := Parse(`{"big":12345678901234567890}`, UseNumber())
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	m := got.(map[string]any)
	n, ok := m["big"].(json.Number)
	if !ok {
		t.Fatalf("value type = %T, want json.Number", m["big"])
	}
	if n.String() != "12345678901234567890" {
		t.Errorf("value = %q, want exact digits preserved", n.String())
	}
}

func TestParseIsFaultTolerant(t *testing.T) {
	got, errs := Parse(`{"a":1 "b":2}`)
	if len(errs) == 0 {
		t.Error("want at least one error")
	}
	want := map[string]any{"a": float64(1), "b": float64(2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %#v, want %#v (recovery must keep both properties)", got, want)
	}
}

func TestParseNeverReturnsGoError(t *testing.T) {
	// compile-time check: the second result is []ParseError, not error
	var _ func(string, ...ParseOption) (any, []ParseError) = Parse
}

func assertValidParse(t *testing.T, in string, want any, opts ...ParseOption) {
	t.Helper()
	got, errs := Parse(in, opts...)
	if len(errs) != 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("Parse(%q) = %#v, %v; want %#v and no errors", in, got, errs, want)
	}
}

func assertInvalidParse(t *testing.T, in string, want any, opts ...ParseOption) {
	t.Helper()
	got, errs := Parse(in, opts...)
	if len(errs) == 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("Parse(%q) = %#v, %v; want %#v and errors", in, got, errs, want)
	}
}

type obj = map[string]any
type arr = []any

// Port of upstream's "parse: literals".
func TestUpstreamParseLiterals(t *testing.T) {
	assertValidParse(t, "true", true)
	assertValidParse(t, "false", false)
	assertValidParse(t, "null", nil)
	assertValidParse(t, `"foo"`, "foo")
	assertValidParse(t, `"\"-\\-\/-\b-\f-\n-\r-\t"`, "\"-\\-/-\b-\f-\n-\r-\t")
	assertValidParse(t, `"Ü"`, "Ü")
	assertValidParse(t, "9", 9.0)
	assertValidParse(t, "-9", -9.0)
	assertValidParse(t, "0.129", 0.129)
	assertValidParse(t, "23e3", 23e3)
	assertValidParse(t, "1.2E+3", 1.2e+3)
	assertValidParse(t, "1.2E-3", 1.2e-3)
	assertValidParse(t, "1.2E-3 // comment", 1.2e-3)
}

// Port of upstream's "parse: objects".
func TestUpstreamParseObjects(t *testing.T) {
	assertValidParse(t, "{}", obj{})
	assertValidParse(t, `{ "foo": true }`, obj{"foo": true})
	assertValidParse(t, `{ "bar": 8, "xoo": "foo" }`, obj{"bar": 8.0, "xoo": "foo"})
	assertValidParse(t, `{ "hello": [], "world": {} }`, obj{"hello": arr{}, "world": obj{}})
	assertValidParse(t, `{ "a": false, "b": true, "c": [ 7.4 ] }`, obj{"a": false, "b": true, "c": arr{7.4}})
	assertValidParse(t, `{ "lineComment": "//", "blockComment": ["/*", "*/"], "brackets": [ ["{", "}"], ["[", "]"], ["(", ")"] ] }`,
		obj{"lineComment": "//", "blockComment": arr{"/*", "*/"}, "brackets": arr{arr{"{", "}"}, arr{"[", "]"}, arr{"(", ")"}}})
	assertValidParse(t, `{ "hello": { "again": { "inside": 5 }, "world": 1 }}`, obj{"hello": obj{"again": obj{"inside": 5.0}, "world": 1.0}})
	assertValidParse(t, `{ "foo": /*hello*/true }`, obj{"foo": true})
	assertValidParse(t, `{ "": true }`, obj{"": true})
}

// Port of upstream's "parse: arrays".
func TestUpstreamParseArrays(t *testing.T) {
	assertValidParse(t, "[]", arr{})
	assertValidParse(t, "[ [],  [ [] ]]", arr{arr{}, arr{arr{}}})
	assertValidParse(t, "[ 1, 2, 3 ]", arr{1.0, 2.0, 3.0})
	assertValidParse(t, `[ { "a": null } ]`, arr{obj{"a": nil}})
}

// Ports of upstream's "parse: objects with errors", "parse: array with
// errors" and "parse: errors".
func TestUpstreamParseErrors(t *testing.T) {
	assertInvalidParse(t, "{,}", obj{})
	assertInvalidParse(t, `{ "foo": true, }`, obj{"foo": true})
	assertInvalidParse(t, `{ "bar": 8 "xoo": "foo" }`, obj{"bar": 8.0, "xoo": "foo"})
	assertInvalidParse(t, `{ ,"bar": 8 }`, obj{"bar": 8.0})
	assertInvalidParse(t, `{ ,"bar": 8, "foo" }`, obj{"bar": 8.0})
	assertInvalidParse(t, `{ "bar": 8, "foo": }`, obj{"bar": 8.0})
	assertInvalidParse(t, `{ 8, "foo": 9 }`, obj{"foo": 9.0})

	assertInvalidParse(t, "[,]", arr{})
	assertInvalidParse(t, "[ 1 2, 3 ]", arr{1.0, 2.0, 3.0})
	assertInvalidParse(t, "[ ,1, 2, 3 ]", arr{1.0, 2.0, 3.0})
	assertInvalidParse(t, "[ ,1, 2, 3, ]", arr{1.0, 2.0, 3.0})

	assertInvalidParse(t, "", nil)
	assertInvalidParse(t, "1,1", 1.0)
}

// Port of upstream's "parse: disallow comments".
func TestUpstreamParseDisallowComments(t *testing.T) {
	assertValidParse(t, `[ 1, 2, null, "foo" ]`, arr{1.0, 2.0, nil, "foo"}, DisallowComments())
	assertValidParse(t, `{ "hello": [], "world": {} }`, obj{"hello": arr{}, "world": obj{}}, DisallowComments())
	assertInvalidParse(t, `{ "foo": /*comment*/ true }`, obj{"foo": true}, DisallowComments())
}

// Port of upstream's "parse: trailing comma".
func TestUpstreamParseTrailingComma(t *testing.T) {
	assertValidParse(t, `{ "hello": [], }`, obj{"hello": arr{}}, AllowTrailingComma())
	assertValidParse(t, `{ "hello": [] }`, obj{"hello": arr{}}, AllowTrailingComma())
	assertValidParse(t, `{ "hello": [], "world": {}, }`, obj{"hello": arr{}, "world": obj{}}, AllowTrailingComma())
	assertValidParse(t, `{ "hello": [], "world": {} }`, obj{"hello": arr{}, "world": obj{}}, AllowTrailingComma())
	assertValidParse(t, "[ 1, 2, ]", arr{1.0, 2.0}, AllowTrailingComma())
	assertValidParse(t, "[ 1, 2 ]", arr{1.0, 2.0}, AllowTrailingComma())

	assertInvalidParse(t, `{ "hello": [], }`, obj{"hello": arr{}})
	assertInvalidParse(t, `{ "hello": [], "world": {}, }`, obj{"hello": arr{}, "world": obj{}})
	assertInvalidParse(t, "[ 1, 2, ]", arr{1.0, 2.0})
}

// ev is one visitor callback as upstream's assertVisit records it. Path is
// only recorded where upstream passes a path supplier.
type ev struct {
	ID         string
	Text       string
	Line, Char int
	Arg        any
	Path       Path
}

// ve is one visitor error as upstream's assertVisit records it.
type ve struct {
	Code           ParseErrorCode
	Offset, Length int
	Line, Char     int
}

// assertVisit is upstream's assertVisit.
func assertVisit(t *testing.T, in string, want []ev, wantErrs []ve, opts ...ParseOption) {
	t.Helper()
	assertVisitStopping(t, in, nil, want, wantErrs, opts...)
}

// assertVisitStopping is upstream's assertVisit with stopOffsets: a Begin at
// one of those offsets returns false.
func assertVisitStopping(t *testing.T, in string, stops []int, want []ev, wantErrs []ve, opts ...ParseOption) {
	t.Helper()
	var got []ev
	var gotErrs []ve
	record := func(id string, s Span, arg any, withPath bool) {
		e := ev{ID: id, Text: in[s.Offset : s.Offset+s.Length], Line: s.StartLine, Char: s.StartCol, Arg: arg}
		if withPath {
			e.Path = s.Path()
		}
		got = append(got, e)
	}
	Visit(in, Visitor{
		OnObjectBegin: func(s Span) bool {
			record("onObjectBegin", s, nil, true)
			return !slices.Contains(stops, s.Offset)
		},
		OnObjectProperty: func(name string, s Span) { record("onObjectProperty", s, name, true) },
		OnObjectEnd:      func(s Span) { record("onObjectEnd", s, nil, false) },
		OnArrayBegin: func(s Span) bool {
			record("onArrayBegin", s, nil, true)
			return !slices.Contains(stops, s.Offset)
		},
		OnArrayEnd:     func(s Span) { record("onArrayEnd", s, nil, false) },
		OnLiteralValue: func(v any, s Span) { record("onLiteralValue", s, v, true) },
		OnSeparator:    func(sep string, s Span) { record("onSeparator", s, sep, false) },
		OnComment:      func(s Span) { record("onComment", s, nil, false) },
		OnError: func(c ParseErrorCode, s Span) {
			gotErrs = append(gotErrs, ve{c, s.Offset, s.Length, s.StartLine, s.StartCol})
		},
	}, opts...)
	if !reflect.DeepEqual(gotErrs, wantErrs) {
		t.Errorf("Visit(%q) errors:\n got %v\nwant %v", in, gotErrs, wantErrs)
	}
	if len(got) != len(want) {
		t.Errorf("Visit(%q): %d events, want %d:\n got %v\nwant %v", in, len(got), len(want), got, want)
		return
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("Visit(%q) event %d:\n got %+v\nwant %+v", in, i, got[i], want[i])
		}
	}
}

// Port of upstream's "visit: object".
func TestUpstreamVisitObject(t *testing.T) {
	assertVisit(t, "{ }", []ev{
		{"onObjectBegin", "{", 0, 0, nil, Path{}},
		{"onObjectEnd", "}", 0, 2, nil, nil},
	}, nil)
	assertVisit(t, `{ "foo": "bar" }`, []ev{
		{"onObjectBegin", "{", 0, 0, nil, Path{}},
		{"onObjectProperty", `"foo"`, 0, 2, "foo", Path{}},
		{"onSeparator", ":", 0, 7, ":", nil},
		{"onLiteralValue", `"bar"`, 0, 9, "bar", Path{"foo"}},
		{"onObjectEnd", "}", 0, 15, nil, nil},
	}, nil)
	assertVisit(t, `{ "foo": { "goo": 3 } }`, []ev{
		{"onObjectBegin", "{", 0, 0, nil, Path{}},
		{"onObjectProperty", `"foo"`, 0, 2, "foo", Path{}},
		{"onSeparator", ":", 0, 7, ":", nil},
		{"onObjectBegin", "{", 0, 9, nil, Path{"foo"}},
		{"onObjectProperty", `"goo"`, 0, 11, "goo", Path{"foo"}},
		{"onSeparator", ":", 0, 16, ":", nil},
		{"onLiteralValue", "3", 0, 18, 3.0, Path{"foo", "goo"}},
		{"onObjectEnd", "}", 0, 20, nil, nil},
		{"onObjectEnd", "}", 0, 22, nil, nil},
	}, nil)
	assertVisitStopping(t, `{ "foo": "bar", "a": {"b": "c"} }`, []int{0}, []ev{
		{"onObjectBegin", "{", 0, 0, nil, Path{}},
		{"onObjectEnd", "}", 0, 32, nil, nil},
	}, nil)
	assertVisitStopping(t, `{ "a": { "b": "c", "d": { "e": "f" } } }`, []int{7}, []ev{
		{"onObjectBegin", "{", 0, 0, nil, Path{}},
		{"onObjectProperty", `"a"`, 0, 2, "a", Path{}},
		{"onSeparator", ":", 0, 5, ":", nil},
		{"onObjectBegin", "{", 0, 7, nil, Path{"a"}},
		{"onObjectEnd", "}", 0, 37, nil, nil},
		{"onObjectEnd", "}", 0, 39, nil, nil},
	}, nil, DisallowComments())
}

// Port of upstream's "visit: array".
func TestUpstreamVisitArray(t *testing.T) {
	assertVisit(t, "[]", []ev{
		{"onArrayBegin", "[", 0, 0, nil, Path{}},
		{"onArrayEnd", "]", 0, 1, nil, nil},
	}, nil)
	assertVisit(t, "[ true, null, [] ]", []ev{
		{"onArrayBegin", "[", 0, 0, nil, Path{}},
		{"onLiteralValue", "true", 0, 2, true, Path{0}},
		{"onSeparator", ",", 0, 6, ",", nil},
		{"onLiteralValue", "null", 0, 8, nil, Path{1}},
		{"onSeparator", ",", 0, 12, ",", nil},
		{"onArrayBegin", "[", 0, 14, nil, Path{2}},
		{"onArrayEnd", "]", 0, 15, nil, nil},
		{"onArrayEnd", "]", 0, 17, nil, nil},
	}, nil)
	assertVisit(t, "[\r\n0,\r\n1,\r\n2\r\n]", []ev{
		{"onArrayBegin", "[", 0, 0, nil, Path{}},
		{"onLiteralValue", "0", 1, 0, 0.0, Path{0}},
		{"onSeparator", ",", 1, 1, ",", nil},
		{"onLiteralValue", "1", 2, 0, 1.0, Path{1}},
		{"onSeparator", ",", 2, 1, ",", nil},
		{"onLiteralValue", "2", 3, 0, 2.0, Path{2}},
		{"onArrayEnd", "]", 4, 0, nil, nil},
	}, nil)
}

// Port of upstream's "visit: object & array".
func TestUpstreamVisitObjectAndArray(t *testing.T) {
	assertVisit(t, `[ { "p1": [ { "p11": 1, "p12": [ true, [ false, 2 ] ] } ] } ]`, []ev{
		{"onArrayBegin", "[", 0, 0, nil, Path{}},
		{"onObjectBegin", "{", 0, 2, nil, Path{0}},
		{"onObjectProperty", `"p1"`, 0, 4, "p1", Path{0}},
		{"onSeparator", ":", 0, 8, ":", nil},
		{"onArrayBegin", "[", 0, 10, nil, Path{0, "p1"}},
		{"onObjectBegin", "{", 0, 12, nil, Path{0, "p1", 0}},
		{"onObjectProperty", `"p11"`, 0, 14, "p11", Path{0, "p1", 0}},
		{"onSeparator", ":", 0, 19, ":", nil},
		{"onLiteralValue", "1", 0, 21, 1.0, Path{0, "p1", 0, "p11"}},
		{"onSeparator", ",", 0, 22, ",", nil},
		{"onObjectProperty", `"p12"`, 0, 24, "p12", Path{0, "p1", 0}},
		{"onSeparator", ":", 0, 29, ":", nil},
		{"onArrayBegin", "[", 0, 31, nil, Path{0, "p1", 0, "p12"}},
		{"onLiteralValue", "true", 0, 33, true, Path{0, "p1", 0, "p12", 0}},
		{"onSeparator", ",", 0, 37, ",", nil},
		{"onArrayBegin", "[", 0, 39, nil, Path{0, "p1", 0, "p12", 1}},
		{"onLiteralValue", "false", 0, 41, false, Path{0, "p1", 0, "p12", 1, 0}},
		{"onSeparator", ",", 0, 46, ",", nil},
		{"onLiteralValue", "2", 0, 48, 2.0, Path{0, "p1", 0, "p12", 1, 1}},
		{"onArrayEnd", "]", 0, 50, nil, nil},
		{"onArrayEnd", "]", 0, 52, nil, nil},
		{"onObjectEnd", "}", 0, 54, nil, nil},
		{"onArrayEnd", "]", 0, 56, nil, nil},
		{"onObjectEnd", "}", 0, 58, nil, nil},
		{"onArrayEnd", "]", 0, 60, nil, nil},
	}, nil)
	assertVisitStopping(t, `{ "foo": [ { "a": "b", "c:": "d", "d": { "e": "f" } } ] }`, []int{9}, []ev{
		{"onObjectBegin", "{", 0, 0, nil, Path{}},
		{"onObjectProperty", `"foo"`, 0, 2, "foo", Path{}},
		{"onSeparator", ":", 0, 7, ":", nil},
		{"onArrayBegin", "[", 0, 9, nil, Path{"foo"}},
		{"onArrayEnd", "]", 0, 54, nil, nil},
		{"onObjectEnd", "}", 0, 56, nil, nil},
	}, nil, DisallowComments())
}

// Port of upstream's "visit: comment".
func TestUpstreamVisitComment(t *testing.T) {
	assertVisit(t, "/* g */ { \"foo\": //f\n\"bar\" }", []ev{
		{"onComment", "/* g */", 0, 0, nil, nil},
		{"onObjectBegin", "{", 0, 8, nil, Path{}},
		{"onObjectProperty", `"foo"`, 0, 10, "foo", Path{}},
		{"onSeparator", ":", 0, 15, ":", nil},
		{"onComment", "//f", 0, 17, nil, nil},
		{"onLiteralValue", `"bar"`, 1, 0, "bar", Path{"foo"}},
		{"onObjectEnd", "}", 1, 6, nil, nil},
	}, nil)
	assertVisit(t, "/* g\r\n */ { \"foo\": //f\n\"bar\" }", []ev{
		{"onComment", "/* g\r\n */", 0, 0, nil, nil},
		{"onObjectBegin", "{", 1, 4, nil, Path{}},
		{"onObjectProperty", `"foo"`, 1, 6, "foo", Path{}},
		{"onSeparator", ":", 1, 11, ":", nil},
		{"onComment", "//f", 1, 13, nil, nil},
		{"onLiteralValue", `"bar"`, 2, 0, "bar", Path{"foo"}},
		{"onObjectEnd", "}", 2, 6, nil, nil},
	}, nil)
	assertVisit(t, "/* g\n */ { \"foo\": //f\n\"bar\"\n}", []ev{
		{"onObjectBegin", "{", 1, 4, nil, Path{}},
		{"onObjectProperty", `"foo"`, 1, 6, "foo", Path{}},
		{"onSeparator", ":", 1, 11, ":", nil},
		{"onLiteralValue", `"bar"`, 2, 0, "bar", Path{"foo"}},
		{"onObjectEnd", "}", 3, 0, nil, nil},
	}, []ve{
		{InvalidCommentToken, 0, 8, 0, 0},
		{InvalidCommentToken, 18, 3, 1, 13},
	}, DisallowComments())
}

// Port of upstream's "visit: malformed". Upstream notes these calls depend on
// implementation details rather than dictating how malformed input parses.
func TestUpstreamVisitMalformed(t *testing.T) {
	assertVisit(t, `[ { "a", "b": [] 1, "c" [ 1 2 ], true: "d":, "e": }, 2, 3 4 ]`, []ev{
		{"onArrayBegin", "[", 0, 0, nil, Path{}},
		{"onObjectBegin", "{", 0, 2, nil, Path{0}},
		{"onObjectProperty", `"a"`, 0, 4, "a", Path{0}},
		{"onSeparator", ",", 0, 7, ",", nil},
		{"onObjectProperty", `"b"`, 0, 9, "b", Path{0}},
		{"onSeparator", ":", 0, 12, ":", nil},
		{"onArrayBegin", "[", 0, 14, nil, Path{0, "b"}},
		{"onArrayEnd", "]", 0, 15, nil, nil},
		{"onSeparator", ",", 0, 18, ",", nil},
		{"onObjectProperty", `"c"`, 0, 20, "c", Path{0}},
		{"onSeparator", ",", 0, 31, ",", nil},
		{"onSeparator", ",", 0, 43, ",", nil},
		{"onObjectProperty", `"e"`, 0, 45, "e", Path{0}},
		{"onSeparator", ":", 0, 48, ":", nil},
		{"onObjectEnd", "}", 0, 50, nil, nil},
		{"onSeparator", ",", 0, 51, ",", nil},
		{"onLiteralValue", "2", 0, 53, 2.0, Path{1}},
		{"onSeparator", ",", 0, 54, ",", nil},
		{"onLiteralValue", "3", 0, 56, 3.0, Path{2}},
		{"onLiteralValue", "4", 0, 58, 4.0, Path{3}},
		{"onArrayEnd", "]", 0, 60, nil, nil},
	}, []ve{
		{ColonExpected, 7, 1, 0, 7},
		{CommaExpected, 17, 1, 0, 17},
		{PropertyNameExpected, 17, 1, 0, 17},
		{ValueExpected, 18, 1, 0, 18},
		{ColonExpected, 24, 1, 0, 24},
		{PropertyNameExpected, 33, 4, 0, 33},
		{ValueExpected, 43, 1, 0, 43},
		{ValueExpected, 50, 1, 0, 50},
		{CommaExpected, 58, 1, 0, 58},
	})
}

// Port of the visit half of upstream's "visit: incomplete".
func TestUpstreamVisitIncomplete(t *testing.T) {
	assertVisit(t, `{"prop1":"foo","prop2":"foo2","prop3":{"prp1":{""}}}`, []ev{
		{"onObjectBegin", "{", 0, 0, nil, Path{}},
		{"onObjectProperty", `"prop1"`, 0, 1, "prop1", Path{}},
		{"onSeparator", ":", 0, 8, ":", nil},
		{"onLiteralValue", `"foo"`, 0, 9, "foo", Path{"prop1"}},
		{"onSeparator", ",", 0, 14, ",", nil},
		{"onObjectProperty", `"prop2"`, 0, 15, "prop2", Path{}},
		{"onSeparator", ":", 0, 22, ":", nil},
		{"onLiteralValue", `"foo2"`, 0, 23, "foo2", Path{"prop2"}},
		{"onSeparator", ",", 0, 29, ",", nil},
		{"onObjectProperty", `"prop3"`, 0, 30, "prop3", Path{}},
		{"onSeparator", ":", 0, 37, ":", nil},
		{"onObjectBegin", "{", 0, 38, nil, Path{"prop3"}},
		{"onObjectProperty", `"prp1"`, 0, 39, "prp1", Path{"prop3"}},
		{"onSeparator", ":", 0, 45, ":", nil},
		{"onObjectBegin", "{", 0, 46, nil, Path{"prop3", "prp1"}},
		{"onObjectProperty", `""`, 0, 47, "", Path{"prop3", "prp1"}},
		{"onObjectEnd", "}", 0, 49, nil, nil},
		{"onObjectEnd", "}", 0, 50, nil, nil},
		{"onObjectEnd", "}", 0, 51, nil, nil},
	}, []ve{{ColonExpected, 49, 1, 0, 49}})
}

// A skipped container's errors are skipped too, and its End still fires.
func TestVisitSkipSuppressesErrors(t *testing.T) {
	assertVisitStopping(t, "[ [1 2], 3 4 ]", []int{2}, []ev{
		{"onArrayBegin", "[", 0, 0, nil, Path{}},
		{"onArrayBegin", "[", 0, 2, nil, Path{0}},
		{"onArrayEnd", "]", 0, 6, nil, nil},
		{"onSeparator", ",", 0, 7, ",", nil},
		{"onLiteralValue", "3", 0, 9, 3.0, Path{1}},
		{"onLiteralValue", "4", 0, 11, 4.0, Path{2}},
		{"onArrayEnd", "]", 0, 13, nil, nil},
	}, []ve{{CommaExpected, 11, 1, 0, 11}})
}

// Depth is counted with no OnObjectBegin set; upstream only counts it when one
// is, so there the inner object's End would end the skip.
func TestVisitSkipCountsDepthWithoutBeginCallbacks(t *testing.T) {
	var ends []string
	Visit(`[{}, 1]`, Visitor{
		OnArrayBegin:   func(Span) bool { return false },
		OnObjectEnd:    func(Span) { ends = append(ends, "}") },
		OnArrayEnd:     func(Span) { ends = append(ends, "]") },
		OnLiteralValue: func(any, Span) { ends = append(ends, "1") },
	})
	if want := []string{"]"}; !reflect.DeepEqual(ends, want) {
		t.Errorf("got %v, want %v", ends, want)
	}
}

// "1." keeps its value as 1, as upstream; json.Number("1.") wouldn't marshal.
func TestParseUseNumberTruncatedDecimal(t *testing.T) {
	v, _ := Parse("[1.]", UseNumber())
	if !reflect.DeepEqual(v, arr{json.Number("1")}) {
		t.Errorf("got %#v", v)
	}
	if _, err := json.Marshal(v); err != nil {
		t.Error(err)
	}
}
