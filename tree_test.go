package jsonc

import (
	"reflect"
	"testing"
)

func TestParseTreeShape(t *testing.T) {
	root, errs := ParseTree(`{"a":[1,true]}`)
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if root.Type != NodeObject {
		t.Fatalf("root type = %v, want NodeObject", root.Type)
	}
	if len(root.Children) != 1 {
		t.Fatalf("root has %d children, want 1", len(root.Children))
	}
	prop := root.Children[0]
	if prop.Type != NodeProperty {
		t.Fatalf("child type = %v, want NodeProperty", prop.Type)
	}
	if len(prop.Children) != 2 {
		t.Fatalf("property has %d children, want 2 (key and value)", len(prop.Children))
	}
	if prop.Children[0].Type != NodeString || prop.Children[0].Literal != "a" {
		t.Errorf("key node = %v/%v, want NodeString/a", prop.Children[0].Type, prop.Children[0].Literal)
	}
	arr := prop.Children[1]
	if arr.Type != NodeArray || len(arr.Children) != 2 {
		t.Fatalf("value node = %v with %d children, want NodeArray with 2", arr.Type, len(arr.Children))
	}
	if arr.Children[0].Literal != float64(1) || arr.Children[1].Literal != true {
		t.Errorf("array literals = %v,%v want 1,true", arr.Children[0].Literal, arr.Children[1].Literal)
	}
}

func TestNodeOffsetsCoverSource(t *testing.T) {
	text := `{"a": [1, 2]}`
	root, _ := ParseTree(text)
	if got := text[root.Offset : root.Offset+root.Length]; got != text {
		t.Errorf("root span = %q, want the whole document", got)
	}
	arr := root.Children[0].Children[1]
	if got := text[arr.Offset : arr.Offset+arr.Length]; got != `[1, 2]` {
		t.Errorf("array span = %q, want %q", got, `[1, 2]`)
	}
	prop := root.Children[0]
	if text[prop.ColonOffset] != ':' {
		t.Errorf("ColonOffset %d points at %q, want ':'", prop.ColonOffset, text[prop.ColonOffset])
	}
}

func TestNodeColonOffsetDefault(t *testing.T) {
	root, _ := ParseTree(`[1]`)
	if root.ColonOffset != -1 {
		t.Errorf("non-property ColonOffset = %d, want -1", root.ColonOffset)
	}
}

func TestNodeParentLinks(t *testing.T) {
	root, _ := ParseTree(`{"a":{"b":1}}`)
	inner := root.Children[0].Children[1]
	if inner.Parent != root.Children[0] {
		t.Error("inner object's Parent is not the property node")
	}
	if root.Parent != nil {
		t.Error("root.Parent should be nil")
	}
}

func TestNodeValue(t *testing.T) {
	root, _ := ParseTree(`{"a":[1,{"b":null}],"c":"x"}`)
	want := map[string]any{
		"a": []any{float64(1), map[string]any{"b": nil}},
		"c": "x",
	}
	if got := root.Value(); !reflect.DeepEqual(got, want) {
		t.Errorf("root.Value() = %#v, want %#v", got, want)
	}
	// A scalar node materializes to its own value.
	if got := root.Children[1].Children[1].Value(); got != "x" {
		t.Errorf("scalar Value() = %v, want x", got)
	}
}

func TestNodeValueMatchesParse(t *testing.T) {
	text := `{"a":[1,2],"b":{"c":true}}`
	root, _ := ParseTree(text)
	parsed, _ := Parse(text)
	if !reflect.DeepEqual(root.Value(), parsed) {
		t.Errorf("root.Value() = %#v, Parse = %#v; they must agree", root.Value(), parsed)
	}
}

func TestNodePath(t *testing.T) {
	root, _ := ParseTree(`{"a":[10,{"b":true}]}`)
	deep := root.Children[0].Children[1].Children[1].Children[0].Children[1]
	if deep.Literal != true {
		t.Fatalf("test navigated to the wrong node: %v", deep.Literal)
	}
	want := Path{"a", 1, "b"}
	if got := deep.Path(); !reflect.DeepEqual(got, want) {
		t.Errorf("Path() = %v, want %v", got, want)
	}
	if got := root.Path(); len(got) != 0 {
		t.Errorf("root.Path() = %v, want empty", got)
	}
}

func TestParseTreeIsFaultTolerant(t *testing.T) {
	root, errs := ParseTree(`{"a":1`)
	if len(errs) == 0 {
		t.Error("want an error for the unterminated object")
	}
	if root == nil {
		t.Fatal("ParseTree returned nil; it must return a best-effort tree")
	}
	if len(root.Children) != 1 {
		t.Errorf("root has %d children, want 1", len(root.Children))
	}
}

func TestParseTreeEmptyInput(t *testing.T) {
	root, errs := ParseTree(``)
	if root != nil {
		t.Errorf("root = %v, want nil for empty input", root)
	}
	if len(errs) == 0 {
		t.Error("want ValueExpected for empty input")
	}
}

func TestParseTreeMissingPropertyValue(t *testing.T) {
	// A property whose value never arrives must still close, or every later
	// event treats it as the enclosing container.
	for _, in := range []string{
		`{"a":}`, `{"a":,"b":2}`, `{"a":{"b":}}`, `{"a":1,"b":}`,
		`{"a":`, `{"a": "b":2}`, `{"a":1 "b":2}`,
	} {
		root, _ := ParseTree(in)
		if root == nil {
			t.Errorf("ParseTree(%q) = nil, want a best-effort tree", in)
			continue
		}
		// Node.Value() must agree with Parse on the same text.
		p, _ := Parse(in)
		if !reflect.DeepEqual(root.Value(), p) {
			t.Errorf("ParseTree(%q).Value() = %#v, Parse = %#v", in, root.Value(), p)
		}
		// Every child's span must sit inside its parent's span.
		var check func(n *Node)
		check = func(n *Node) {
			for _, c := range n.Children {
				if c.Offset < n.Offset || c.Offset+c.Length > n.Offset+n.Length {
					t.Errorf("ParseTree(%q): %v [%d,%d) is outside parent %v [%d,%d)",
						in, c.Type, c.Offset, c.Offset+c.Length,
						n.Type, n.Offset, n.Offset+n.Length)
				}
				check(c)
			}
		}
		check(root)
		// The root must span the whole document, not stop at its first byte.
		if root.Offset != 0 || root.Length != len(in) {
			t.Errorf("ParseTree(%q) root spans [%d,%d), want [0,%d)",
				in, root.Offset, root.Offset+root.Length, len(in))
		}
	}
}

func TestDanglingPropertySpanExcludesDelimiter(t *testing.T) {
	// A property whose value never arrives must end BEFORE the delimiter that
	// closed it. If a call site passed s.Offset+s.Length the property would
	// swallow the '}' or ',' -- a regression that every other assertion in
	// this file is structurally blind to.
	cases := []struct{ in, want string }{
		{`{"a":}`, `"a":`},       // closed by OnObjectEnd
		{`{"a":,"b":2}`, `"a":`}, // closed by OnSeparator ","
		{`{"a":`, `"a":`},        // closed at EOF
	}
	for _, tc := range cases {
		root, _ := ParseTree(tc.in)
		if root == nil || len(root.Children) == 0 {
			t.Errorf("ParseTree(%q): expected a property node", tc.in)
			continue
		}
		p := root.Children[0]
		if p.Type != NodeProperty {
			t.Errorf("ParseTree(%q): first child is %v, want NodeProperty", tc.in, p.Type)
			continue
		}
		if got := tc.in[p.Offset : p.Offset+p.Length]; got != tc.want {
			t.Errorf("ParseTree(%q) property span = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFindNodeAtLocation(t *testing.T) {
	root, _ := ParseTree(`{"a":{"b":[1,2]},"c":true}`)
	tests := []struct {
		path Path
		want any // expected Value() of the found node, or nil for "not found"
	}{
		{Path{"a", "b", 0}, float64(1)},
		{Path{"a", "b", 1}, float64(2)},
		{Path{"c"}, true},
		{Path{"a", "b"}, []any{float64(1), float64(2)}},
		{Path{}, map[string]any{"a": map[string]any{"b": []any{float64(1), float64(2)}}, "c": true}},
	}
	for _, tc := range tests {
		n := FindNodeAtLocation(root, tc.path)
		if n == nil {
			t.Errorf("FindNodeAtLocation(%v) = nil, want a node", tc.path)
			continue
		}
		if !reflect.DeepEqual(n.Value(), tc.want) {
			t.Errorf("FindNodeAtLocation(%v).Value() = %#v, want %#v", tc.path, n.Value(), tc.want)
		}
	}

	// The {"c"} case above only proves Value() is correct, which a property
	// node would also satisfy -- Node.Value() on a property delegates to its
	// value child. Pin that the VALUE node itself is returned, not the
	// property that wraps it.
	if n := FindNodeAtLocation(root, Path{"c"}); n == nil || n.Type != NodeBoolean {
		t.Errorf(`FindNodeAtLocation({"c"}) = %v, want a NodeBoolean value node`, n)
	}
}

func TestFindNodeAtLocationMalformedTrees(t *testing.T) {
	// A dangling property (key, no value) is reachable from ordinary
	// fault-tolerant parsing, so navigating one must return nil rather than
	// panic on Children[1].
	root, _ := ParseTree(`{"a":}`)
	if root == nil {
		t.Fatal("ParseTree(`{\"a\":}`) returned nil")
	}
	if n := FindNodeAtLocation(root, Path{"a"}); n != nil {
		t.Errorf("FindNodeAtLocation on a dangling property = %v, want nil", n.Type)
	}
	if n := FindNodeAtLocation(root, Path{"a", "b"}); n != nil {
		t.Errorf("descending through a dangling property = %v, want nil", n.Type)
	}

	// A negative index must be rejected, not used to index Children.
	arr, _ := ParseTree(`{"a":[1,2]}`)
	for _, p := range []Path{{"a", -1}, {"a", -99}} {
		if n := FindNodeAtLocation(arr, p); n != nil {
			t.Errorf("FindNodeAtLocation(%v) = %v, want nil", p, n.Type)
		}
	}
}

func TestFindNodeAtLocationMisses(t *testing.T) {
	root, _ := ParseTree(`{"a":[1]}`)
	for _, p := range []Path{
		{"missing"},
		{"a", 5},
		{"a", "notAnIndex"},
		{"a", 0, "tooDeep"},
		{1.5},  // malformed element: not string or int
		{true}, // malformed element
		{nil},  // malformed element: nil is a realistic input, not contrived --
		// a property whose key is not a string can leave a nil placeholder in
		// a path stack built by calling code.
		{"a", nil}, // malformed element after a valid descent, not just at the root
	} {
		if n := FindNodeAtLocation(root, p); n != nil {
			t.Errorf("FindNodeAtLocation(%v) = %v, want nil", p, n.Type)
		}
	}
	if n := FindNodeAtLocation(nil, Path{"a"}); n != nil {
		t.Error("FindNodeAtLocation(nil, ...) should return nil")
	}
}

func TestFindNodeAtOffset(t *testing.T) {
	//            0123456789...
	text := `{"a": [1, 22]}`
	root, _ := ParseTree(text)

	// Offset 2 is inside the key "a".
	if n := FindNodeAtOffset(root, 2, false); n == nil || n.Literal != "a" {
		t.Errorf("at offset 2 got %v, want the key node", n)
	}
	// Offset 7 is the literal 1.
	if n := FindNodeAtOffset(root, 7, false); n == nil || n.Literal != float64(1) {
		t.Errorf("at offset 7 got %v, want literal 1", n)
	}
	// Offset 11 is inside the literal 22.
	if n := FindNodeAtOffset(root, 11, false); n == nil || n.Literal != float64(22) {
		t.Errorf("at offset 11 got %v, want literal 22", n)
	}
	// Offset 5 is the space before the array: innermost containing node is the property.
	if n := FindNodeAtOffset(root, 5, false); n == nil || n.Type != NodeProperty {
		t.Errorf("at offset 5 got %v, want the property node", n)
	}
}

func TestFindNodeAtOffsetRightBound(t *testing.T) {
	text := `[1]`
	root, _ := ParseTree(text)
	// Offset 2 is immediately after the literal "1".
	if n := FindNodeAtOffset(root, 2, false); n != nil && n.Literal == float64(1) {
		t.Error("without includeRightBound, offset 2 must not match literal 1")
	}
	if n := FindNodeAtOffset(root, 2, true); n == nil || n.Literal != float64(1) {
		t.Errorf("with includeRightBound, offset 2 = %v, want literal 1", n)
	}
}

func TestFindNodeAtOffsetRightBoundAtRoot(t *testing.T) {
	// Same boundary property as TestFindNodeAtOffsetRightBound, but exercised
	// at the outermost node rather than a leaf, so a mutation that only
	// happens to satisfy the innermost comparison still gets caught.
	text := `[1]`
	root, _ := ParseTree(text)
	end := len(text) // offset 3, exactly at the end of the whole document
	if n := FindNodeAtOffset(root, end, false); n != nil {
		t.Errorf("without includeRightBound, offset %d (== doc end) = %v, want nil", end, n)
	}
	if n := FindNodeAtOffset(root, end, true); n == nil || n.Type != NodeArray {
		t.Errorf("with includeRightBound, offset %d (== doc end) = %v, want the root array", end, n)
	}
}

func TestFindNodeAtOffsetOutOfRange(t *testing.T) {
	root, _ := ParseTree(`[1]`)
	if n := FindNodeAtOffset(root, 999, false); n != nil {
		t.Errorf("offset past end = %v, want nil", n)
	}
	if n := FindNodeAtOffset(nil, 0, false); n != nil {
		t.Error("nil root should return nil")
	}
}
