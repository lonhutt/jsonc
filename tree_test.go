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
