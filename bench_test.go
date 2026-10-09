package jsonc

import (
	"strings"
	"testing"
)

// benchDoc is a settings-style document: comments, nesting a few levels deep,
// and some arrays.
var benchDoc = func() string {
	var b strings.Builder
	b.WriteString("{\n")
	for i := range 200 {
		b.WriteString(`  // entry
  "key` + string(rune('a'+i%26)) + `": {"enabled": true, "level": 3, "tags": ["x", "y", {"deep": [1, 2, {"deeper": null}]}]},
`)
	}
	b.WriteString(`  "last": 0
}`)
	return b.String()
}()

func BenchmarkParse(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchDoc)))
	for b.Loop() {
		Parse(benchDoc)
	}
}

func BenchmarkParseTree(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchDoc)))
	for b.Loop() {
		ParseTree(benchDoc)
	}
}

func BenchmarkGetLocationNearStart(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		GetLocation(benchDoc, 30)
	}
}

func BenchmarkFormatRangeNearEnd(b *testing.B) {
	b.ReportAllocs()
	r := Range{Offset: len(benchDoc) - 20, Length: 10}
	for b.Loop() {
		Format(benchDoc, &r, DefaultFormatOptions())
	}
}
