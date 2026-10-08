package jsonc

import (
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
		// Ties at one offset, as node-jsonc-parser 3.3.1 resolves them.
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

// Two tied edits cannot catch an unstable sort: Go sorts short slices by
// insertion, which is stable anyway. Enough ties, mixed with other offsets,
// can.
func TestApplyEditsManyInsertionsKeepOrder(t *testing.T) {
	var edits []Edit
	var want strings.Builder
	for i := range 100 {
		edits = append(edits, Edit{Offset: 1, Length: 0, Content: strconv.Itoa(i) + ","})
		want.WriteString(strconv.Itoa(i) + ",")
		// Interleave no-op edits at two other offsets, so the sort has real
		// work to do around the ties.
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
