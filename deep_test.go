package jsonc

import (
	"runtime/debug"
	"strings"
	"testing"
)

// Deep nesting can't overflow the stack. With the stack capped at 1MB, a
// recursive parser dies here, and Go can't recover from that.
func TestDeepNesting(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(1 << 20))
	const depth = 100_000
	text := strings.Repeat(`[{"a":`, depth) + "1" + strings.Repeat("}]", depth)

	if _, errs := Parse(text); len(errs) != 0 {
		t.Fatalf("Parse: %v", errs[0])
	}
	root, errs := ParseTree(text)
	if len(errs) != 0 {
		t.Fatalf("ParseTree: %v", errs[0])
	}
	path := make(Path, 0, 2*depth)
	for range depth {
		path = append(path, 0, "a")
	}
	n := FindNodeAtLocation(root, path)
	if n == nil || n.Value() != 1.0 || len(n.Path()) != 2*depth {
		t.Fatal("deepest value not found")
	}
	if v, ok := root.Value().([]any); !ok || len(v) != 1 {
		t.Fatalf("root value is %T", root.Value())
	}
	if loc := GetLocation(text, strings.Index(text, "1")+1); len(loc.Path) != 2*depth {
		t.Fatalf("GetLocation path has %d segments", len(loc.Path))
	}
	if _, err := Modify(text, path, 2, ModifyOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(text, path, ModifyOptions{}); err != nil {
		t.Fatal(err)
	}
}
