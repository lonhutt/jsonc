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
	// a property whose value never arrives still has to close, or later events
	// treat it as the container
	for _, in := range []string{
		`{"a":}`, `{"a":,"b":2}`, `{"a":{"b":}}`, `{"a":1,"b":}`,
		`{"a":`, `{"a": "b":2}`, `{"a":1 "b":2}`,
	} {
		root, _ := ParseTree(in)
		if root == nil {
			t.Errorf("ParseTree(%q) = nil, want a best-effort tree", in)
			continue
		}
		p, _ := Parse(in)
		if !reflect.DeepEqual(root.Value(), p) {
			t.Errorf("ParseTree(%q).Value() = %#v, Parse = %#v", in, root.Value(), p)
		}
		// every child's span sits inside its parent's
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
		// the root spans the whole document
		if root.Offset != 0 || root.Length != len(in) {
			t.Errorf("ParseTree(%q) root spans [%d,%d), want [0,%d)",
				in, root.Offset, root.Offset+root.Length, len(in))
		}
	}
}

func TestDanglingPropertySpanExcludesDelimiter(t *testing.T) {
	// A dangling property ends before the delimiter that closed it; passing
	// s.Offset+s.Length would swallow the '}' or ',', and nothing else here would
	// notice.
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

	// Value() can't tell a property from its value, since a property's Value
	// delegates; pin the node type.
	if n := FindNodeAtLocation(root, Path{"c"}); n == nil || n.Type != NodeBoolean {
		t.Errorf(`FindNodeAtLocation({"c"}) = %v, want a NodeBoolean value node`, n)
	}
}

func TestFindNodeAtLocationMalformedTrees(t *testing.T) {
	// {"a":} comes out of ordinary parsing; navigating it returns nil instead of
	// panicking on Children[1].
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
		{1.5},      // malformed element: not string or int
		{true},     // malformed element
		{nil},      // a non-string key can leave nil in a caller's path
		{"a", nil}, // after a valid step, not just at the root
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

	if n := FindNodeAtOffset(root, 2, false); n == nil || n.Literal != "a" {
		t.Errorf("at offset 2 got %v, want the key node", n)
	}
	if n := FindNodeAtOffset(root, 7, false); n == nil || n.Literal != float64(1) {
		t.Errorf("at offset 7 got %v, want literal 1", n)
	}
	if n := FindNodeAtOffset(root, 11, false); n == nil || n.Literal != float64(22) {
		t.Errorf("at offset 11 got %v, want literal 22", n)
	}
	// the space before the array; innermost is the property
	if n := FindNodeAtOffset(root, 5, false); n == nil || n.Type != NodeProperty {
		t.Errorf("at offset 5 got %v, want the property node", n)
	}
}

func TestFindNodeAtOffsetRightBound(t *testing.T) {
	text := `[1]`
	root, _ := ParseTree(text)
	// just past the literal 1
	if n := FindNodeAtOffset(root, 2, false); n != nil && n.Literal == float64(1) {
		t.Error("without includeRightBound, offset 2 must not match literal 1")
	}
	if n := FindNodeAtOffset(root, 2, true); n == nil || n.Literal != float64(1) {
		t.Errorf("with includeRightBound, offset 2 = %v, want literal 1", n)
	}
}

func TestFindNodeAtOffsetRightBoundAtRoot(t *testing.T) {
	// Same boundary as TestFindNodeAtOffsetRightBound, but at the root instead
	// of a leaf.
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

// tn is a node as upstream's tree tests write it: Colon 0 means no
// colonOffset, and an empty container has no children.
type tn struct {
	Type           NodeType
	Offset, Length int
	Colon          int
	Value          any
	Children       []tn
}

// te is a tree error as upstream's tests write it.
type te struct {
	Code           ParseErrorCode
	Offset, Length int
}

func toTN(t *testing.T, n *Node) tn {
	t.Helper()
	out := tn{Type: n.Type, Offset: n.Offset, Length: n.Length, Value: n.Literal}
	if n.ColonOffset != -1 {
		out.Colon = n.ColonOffset
	}
	for _, c := range n.Children {
		if c.Parent != n {
			t.Errorf("%v at %d: a child's Parent isn't its parent", c.Type, c.Offset)
		}
		out.Children = append(out.Children, toTN(t, c))
	}
	return out
}

// assertTree is upstream's assertTree.
func assertTree(t *testing.T, in string, want tn, wantErrs ...te) {
	t.Helper()
	root, errs := ParseTree(in)
	var gotErrs []te
	for _, e := range errs {
		gotErrs = append(gotErrs, te{e.Code, e.Offset, e.Length})
	}
	if !reflect.DeepEqual(gotErrs, wantErrs) {
		t.Errorf("ParseTree(%q) errors = %v, want %v", in, gotErrs, wantErrs)
	}
	if got := toTN(t, root); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTree(%q):\n got %+v\nwant %+v", in, got, want)
	}
}

// Port of upstream's "tree: literals".
func TestUpstreamTreeLiterals(t *testing.T) {
	assertTree(t, "true", tn{Type: NodeBoolean, Length: 4, Value: true})
	assertTree(t, "false", tn{Type: NodeBoolean, Length: 5, Value: false})
	assertTree(t, "null", tn{Type: NodeNull, Length: 4})
	assertTree(t, "23", tn{Type: NodeNumber, Length: 2, Value: 23.0})
	assertTree(t, "-1.93e-19", tn{Type: NodeNumber, Length: 9, Value: -1.93e-19})
	assertTree(t, `"hello"`, tn{Type: NodeString, Length: 7, Value: "hello"})
}

// Port of upstream's "tree: arrays".
func TestUpstreamTreeArrays(t *testing.T) {
	assertTree(t, "[]", tn{Type: NodeArray, Length: 2})
	assertTree(t, "[ 1 ]", tn{Type: NodeArray, Length: 5, Children: []tn{{Type: NodeNumber, Offset: 2, Length: 1, Value: 1.0}}})
	assertTree(t, `[ 1,"x"]`, tn{Type: NodeArray, Length: 8, Children: []tn{
		{Type: NodeNumber, Offset: 2, Length: 1, Value: 1.0},
		{Type: NodeString, Offset: 4, Length: 3, Value: "x"},
	}})
	assertTree(t, "[[]]", tn{Type: NodeArray, Length: 4, Children: []tn{{Type: NodeArray, Offset: 1, Length: 2}}})
}

// Port of upstream's "tree: objects".
func TestUpstreamTreeObjects(t *testing.T) {
	assertTree(t, "{ }", tn{Type: NodeObject, Length: 3})
	assertTree(t, `{ "val": 1 }`, tn{Type: NodeObject, Length: 12, Children: []tn{
		{Type: NodeProperty, Offset: 2, Length: 8, Colon: 7, Children: []tn{
			{Type: NodeString, Offset: 2, Length: 5, Value: "val"},
			{Type: NodeNumber, Offset: 9, Length: 1, Value: 1.0},
		}},
	}})
	assertTree(t, `{"id": "$", "v": [ null, null] }`, tn{Type: NodeObject, Length: 32, Children: []tn{
		{Type: NodeProperty, Offset: 1, Length: 9, Colon: 5, Children: []tn{
			{Type: NodeString, Offset: 1, Length: 4, Value: "id"},
			{Type: NodeString, Offset: 7, Length: 3, Value: "$"},
		}},
		{Type: NodeProperty, Offset: 12, Length: 18, Colon: 15, Children: []tn{
			{Type: NodeString, Offset: 12, Length: 3, Value: "v"},
			{Type: NodeArray, Offset: 17, Length: 13, Children: []tn{
				{Type: NodeNull, Offset: 19, Length: 4},
				{Type: NodeNull, Offset: 25, Length: 4},
			}},
		}},
	}})
	assertTree(t, `{  "id": { "foo": { } } , }`, tn{Type: NodeObject, Length: 27, Children: []tn{
		{Type: NodeProperty, Offset: 3, Length: 20, Colon: 7, Children: []tn{
			{Type: NodeString, Offset: 3, Length: 4, Value: "id"},
			{Type: NodeObject, Offset: 9, Length: 14, Children: []tn{
				{Type: NodeProperty, Offset: 11, Length: 10, Colon: 16, Children: []tn{
					{Type: NodeString, Offset: 11, Length: 5, Value: "foo"},
					{Type: NodeObject, Offset: 18, Length: 3},
				}},
			}},
		}},
	}}, te{PropertyNameExpected, 26, 1}, te{ValueExpected, 26, 1})
}

// Port of the tree half of upstream's "visit: incomplete".
func TestUpstreamTreeIncomplete(t *testing.T) {
	assertTree(t, `{"prop1":"foo","prop2":"foo2","prop3":{"prp1":{""}}}`, tn{Type: NodeObject, Length: 52, Children: []tn{
		{Type: NodeProperty, Offset: 1, Length: 13, Colon: 8, Children: []tn{
			{Type: NodeString, Offset: 1, Length: 7, Value: "prop1"},
			{Type: NodeString, Offset: 9, Length: 5, Value: "foo"},
		}},
		{Type: NodeProperty, Offset: 15, Length: 14, Colon: 22, Children: []tn{
			{Type: NodeString, Offset: 15, Length: 7, Value: "prop2"},
			{Type: NodeString, Offset: 23, Length: 6, Value: "foo2"},
		}},
		{Type: NodeProperty, Offset: 30, Length: 21, Colon: 37, Children: []tn{
			{Type: NodeString, Offset: 30, Length: 7, Value: "prop3"},
			{Type: NodeObject, Offset: 38, Length: 13, Children: []tn{
				{Type: NodeProperty, Offset: 39, Length: 11, Colon: 45, Children: []tn{
					{Type: NodeString, Offset: 39, Length: 6, Value: "prp1"},
					{Type: NodeObject, Offset: 46, Length: 4, Children: []tn{
						// upstream's Length is 3, taking the '}' that closes a
						// property with no value; ours stops before it
						{Type: NodeProperty, Offset: 47, Length: 2, Children: []tn{
							{Type: NodeString, Offset: 47, Length: 2, Value: ""},
						}},
					}},
				}},
			}},
		}},
	}}, te{ColonExpected, 49, 1})
}

// Port of upstream's "tree: find location".
func TestUpstreamFindLocation(t *testing.T) {
	root, _ := ParseTree(`{ "key1": { "key11": [ "val111", "val112" ] }, "key2": [ { "key21": false, "key22": 221 }, null, [{}] ], "key3": { "key31":, "key32": 32 } }`)
	for _, tc := range []struct {
		path Path
		want any
	}{
		{Path{"key1"}, obj{"key11": arr{"val111", "val112"}}},
		{Path{"key1", "key11"}, arr{"val111", "val112"}},
		{Path{"key1", "key11", 0}, "val111"},
		{Path{"key1", "key11", 1}, "val112"},
		{Path{"key1", "key11", 2}, nil},
		{Path{"key2", 0, "key21"}, false},
		{Path{"key2", 0, "key22"}, 221.0},
		{Path{"key2", 1}, nil},
		{Path{"key2", 2}, arr{obj{}}},
		{Path{"key2", 2, 0}, obj{}},
		{Path{"key3", "key31", "key311"}, nil},
		{Path{"key3", "key32"}, 32.0},
	} {
		n := FindNodeAtLocation(root, tc.path)
		var got any
		if n != nil {
			got = n.Value()
			if !reflect.DeepEqual(n.Path(), tc.path) {
				t.Errorf("%v: node path is %v", tc.path, n.Path())
			}
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: got %#v, want %#v", tc.path, got, tc.want)
		}
	}
}
