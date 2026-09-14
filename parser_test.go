package jsonc

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// collectErrors runs Visit and returns just the error codes, in order.
func collectErrors(t *testing.T, text string, opts ...ParseOption) []ParseErrorCode {
	t.Helper()
	var codes []ParseErrorCode
	Visit(text, Visitor{
		OnError: func(c ParseErrorCode, _ Span) { codes = append(codes, c) },
	}, opts...)
	return codes
}

// events records the visitor callback sequence as readable strings, which
// makes a failure message show what actually happened.
func events(text string, opts ...ParseOption) []string {
	var out []string
	Visit(text, Visitor{
		OnObjectBegin:    func(Span) { out = append(out, "{") },
		OnObjectEnd:      func(Span) { out = append(out, "}") },
		OnArrayBegin:     func(Span) { out = append(out, "[") },
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
		// All five literal kinds: null decodes to nil, the keywords to bools,
		// the number to a float64, and the string to its DECODED value.
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
	// The key span covers the quoted literal "ab", offsets 1..5.
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
	// A missing comma must resync at the next property, not discard the rest.
	got := events(`{"a":1 "b":2}`)
	want := []string{"{", "key:a", "sep::", "val:1", "key:b", "sep::", "val:2", "}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v (parser must recover and see property b)", got, want)
	}
}

func TestVisitSynthesizesClosers(t *testing.T) {
	// An unterminated object must still fire OnObjectEnd so consumers that
	// maintain a stack can unwind it.
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
	// The colon is missing before the "1" at offset 8, on line 1 (0-based),
	// column 6 (0-based bytes from the start of that line: "  \"a\" 1").
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

	// Out of float64 range: reported and zeroed on the float64 path...
	if v, codes := probe(`1e400`); v != float64(0) || len(codes) != 1 || codes[0] != InvalidNumberFormat {
		t.Errorf("1e400 float64 path = %#v, errs %v; want 0 and [InvalidNumberFormat]", v, codes)
	}
	// ...but preserved exactly under UseNumber, with no error.
	if v, codes := probe(`1e400`, UseNumber()); v != json.Number("1e400") || len(codes) != 0 {
		t.Errorf("1e400 UseNumber = %#v, errs %v; want json.Number(\"1e400\") and no errors", v, codes)
	}
	// A large integer survives UseNumber intact.
	if v, _ := probe(`12345678901234567890`, UseNumber()); v != json.Number("12345678901234567890") {
		t.Errorf("large int UseNumber = %#v, want digits preserved", v)
	}
	// A genuine SYNTAX error is still reported under UseNumber. The scanner
	// already flags "1e" as UnexpectedEndOfNumber before parseLiteral adds
	// InvalidNumberFormat, so both codes come out, in that order.
	if _, codes := probe(`1e`, UseNumber()); len(codes) == 0 {
		t.Error("1e under UseNumber reported no error; a syntax error must still be reported")
	}
}

// Documents the exact codes for "1e" under UseNumber so a change is visible.
func TestVisitNumberSyntaxErrorCodes(t *testing.T) {
	var codes []ParseErrorCode
	Visit(`1e`, Visitor{
		OnError: func(c ParseErrorCode, _ Span) { codes = append(codes, c) },
	}, UseNumber())
	want := []ParseErrorCode{UnexpectedEndOfNumber, InvalidNumberFormat}
	if !reflect.DeepEqual(codes, want) {
		t.Errorf("1e under UseNumber = %v, want %v", codes, want)
	}
}

// I1: Visit's bool return is a contract Task 5's Parse branches on.
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
	// Collect the Spans and call Path() only AFTER Visit returns, by which
	// point the parser's path stack has been fully unwound. If span() ever
	// reads the live stack instead of snapshotting, these come back empty
	// or wrong while TestVisitPathSupplier keeps passing.
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
		// Comments and trailing commas are the point of the library.
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
	// For input that is also valid strict JSON, Parse must agree with the
	// standard library.
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
	// A broken document still yields the parts that parsed.
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
	// Deliberate shape check: Parse has exactly two results and the second is
	// a slice, not an error. This is the library's contract.
	var _ func(string, ...ParseOption) (any, []ParseError) = Parse
}
