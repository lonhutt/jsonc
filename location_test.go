package jsonc

import (
	"reflect"
	"strings"
	"testing"
)

// assertLocation checks the path, the type of PreviousNode, and the
// property-key flag at the offset marked by "|" in marked. It is the Go port
// of upstream's assertLocation helper; wantType is "" when PreviousNode is
// expected to be nil, mirroring upstream's `void 0`.
func assertLocation(t *testing.T, marked string, wantPath Path, wantType NodeType, wantIsKey bool) {
	t.Helper()
	offset := strings.IndexByte(marked, '|')
	if offset < 0 {
		t.Fatalf("test input %q has no | marker", marked)
	}
	text := marked[:offset] + marked[offset+1:]
	loc := GetLocation(text, offset)
	if !reflect.DeepEqual(loc.Path, wantPath) {
		t.Errorf("GetLocation(%q) path = %#v, want %#v", marked, loc.Path, wantPath)
	}
	var gotType NodeType
	if loc.PreviousNode != nil {
		gotType = loc.PreviousNode.Type
	}
	if gotType != wantType {
		t.Errorf("GetLocation(%q) PreviousNode type = %q, want %q", marked, gotType, wantType)
	}
	if loc.IsAtPropertyKey != wantIsKey {
		t.Errorf("GetLocation(%q) IsAtPropertyKey = %v, want %v", marked, loc.IsAtPropertyKey, wantIsKey)
	}
}

// assertMatchesLocation is the port of upstream's helper of the same name: it
// takes a location from a marked document and asserts Matches on it.
func assertMatchesLocation(t *testing.T, marked string, pattern Path, want bool) {
	t.Helper()
	offset := strings.IndexByte(marked, '|')
	if offset < 0 {
		t.Fatalf("test input %q has no | marker", marked)
	}
	text := marked[:offset] + marked[offset+1:]
	loc := GetLocation(text, offset)
	if got := loc.Matches(pattern); got != want {
		t.Errorf("GetLocation(%q).Matches(%#v) = %v, want %v (path %#v)", marked, pattern, got, want, loc.Path)
	}
}

// TestGetLocationProperties is the port of upstream's "location: properties".
func TestGetLocationProperties(t *testing.T) {
	assertLocation(t, `|{ "foo": "bar" }`, Path{}, "", false)
	assertLocation(t, `{| "foo": "bar" }`, Path{""}, "", true)
	assertLocation(t, `{ |"foo": "bar" }`, Path{"foo"}, NodeProperty, true)
	assertLocation(t, `{ "foo|": "bar" }`, Path{"foo"}, NodeProperty, true)
	assertLocation(t, `{ "foo"|: "bar" }`, Path{"foo"}, NodeProperty, true)
	assertLocation(t, `{ "foo": "bar"| }`, Path{"foo"}, NodeString, false)
	assertLocation(t, `{ "foo":| "bar" }`, Path{"foo"}, "", false)
	assertLocation(t, `{ "foo": {"bar|": 1, "car": 2 } }`, Path{"foo", "bar"}, NodeProperty, true)
	assertLocation(t, `{ "foo": {"bar": 1|, "car": 3 } }`, Path{"foo", "bar"}, NodeNumber, false)
	assertLocation(t, `{ "foo": {"bar": 1,| "car": 4 } }`, Path{"foo", ""}, "", true)
	assertLocation(t, `{ "foo": {"bar": 1, "ca|r": 5 } }`, Path{"foo", "car"}, NodeProperty, true)
	assertLocation(t, `{ "foo": {"bar": 1, "car": 6| } }`, Path{"foo", "car"}, NodeNumber, false)
	assertLocation(t, `{ "foo": {"bar": 1, "car": 7 }| }`, Path{"foo"}, "", false)
	assertLocation(t, `{ "foo": {"bar": 1, "car": 8 },| "goo": {} }`, Path{""}, "", true)
	assertLocation(t, `{ "foo": {"bar": 1, "car": 9 }, "go|o": {} }`, Path{"goo"}, NodeProperty, true)
	assertLocation(t, `{ "dep": {"bar": 1, "car": |`, Path{"dep", "car"}, "", false)
	assertLocation(t, `{ "dep": {"bar": 1,, "car": |`, Path{"dep", "car"}, "", false)
	assertLocation(t, `{ "dep": {"bar": "na", "dar": "ma", "car": | } }`, Path{"dep", "car"}, "", false)
}

// TestGetLocationArrays is the port of upstream's "location: arrays".
func TestGetLocationArrays(t *testing.T) {
	assertLocation(t, `|["foo", null ]`, Path{}, "", false)
	assertLocation(t, `[|"foo", null ]`, Path{0}, NodeString, false)
	assertLocation(t, `["foo"|, null ]`, Path{0}, NodeString, false)
	assertLocation(t, `["foo",| null ]`, Path{1}, "", false)
	assertLocation(t, `["foo", |null ]`, Path{1}, NodeNull, false)
	assertLocation(t, `["foo", null,| ]`, Path{2}, "", false)
	assertLocation(t, `["foo", null,,| ]`, Path{3}, "", false)
	assertLocation(t, `[["foo", null,, ],|`, Path{1}, "", false)
}

func TestGetLocation(t *testing.T) {
	assertLocation(t, `{"a": |1}`, Path{"a"}, NodeNumber, false)
	assertLocation(t, `{"a": 1, "b|": 2}`, Path{"b"}, NodeProperty, true)
	assertLocation(t, `{"a": {"b": |true}}`, Path{"a", "b"}, NodeBoolean, false)
	assertLocation(t, `{"a": [1, |2]}`, Path{"a", 1}, NodeNumber, false)
	assertLocation(t, `{"a": [{"b": |1}]}`, Path{"a", 0, "b"}, NodeNumber, false)
	// An incomplete document is the primary use case.
	assertLocation(t, `{"a": |`, Path{"a"}, "", false)
	assertLocation(t, `{"a|`, Path{"a"}, NodeProperty, true)
	// Before any value at all.
	assertLocation(t, `|`, Path{}, "", false)
	assertLocation(t, `|[1, 2]`, Path{}, "", false)
	// A comment before the caret must not disturb the path.
	assertLocation(t, `{"a": /* c */ |1}`, Path{"a"}, NodeNumber, false)
	// A nested array index still advances past a nested container.
	assertLocation(t, `[[1], |2]`, Path{1}, NodeNumber, false)
	// Deep nesting: the array index resets per level rather than leaking.
	assertLocation(t, `[[1, 2], [3, |4]]`, Path{1, 1}, NodeNumber, false)
	// A caret on a token's right edge belongs to that token, not to whatever
	// starts there. Without the closing ']' the next literal begins exactly
	// where the previous one ended.
	assertLocation(t, `[1|"a"]`, Path{0}, NodeNumber, false)
	// A caret sitting exactly on a separator is still before it: the comma has
	// not yet moved the caret on to the next element, and the colon has not
	// yet moved it off the key.
	assertLocation(t, `["foo" |, null ]`, Path{0}, NodeString, false)
	assertLocation(t, `{ "foo" |: "bar" }`, Path{"foo"}, NodeProperty, true)
}

func TestLocationPreviousNode(t *testing.T) {
	loc := GetLocation(`{"a": 123}`, 9) // just after 123, before }
	if loc.PreviousNode == nil {
		t.Fatal("PreviousNode = nil, want the literal node")
	}
	if loc.PreviousNode.Literal != float64(123) {
		t.Errorf("PreviousNode.Literal = %v, want 123", loc.PreviousNode.Literal)
	}
	if loc.PreviousNode.Type != NodeNumber {
		t.Errorf("PreviousNode.Type = %q, want %q", loc.PreviousNode.Type, NodeNumber)
	}
	if loc.PreviousNode.Offset != 6 || loc.PreviousNode.Length != 3 {
		t.Errorf("PreviousNode span = (%d, %d), want (6, 3)", loc.PreviousNode.Offset, loc.PreviousNode.Length)
	}
	if loc.PreviousNode.Parent != nil || loc.PreviousNode.Children != nil {
		t.Error("PreviousNode must be a standalone node, not part of a tree")
	}
}

// TestLocationPreviousNodeIsProperty pins the property case: the node carries
// the key name as its Literal, and its span covers the key token only.
func TestLocationPreviousNodeIsProperty(t *testing.T) {
	loc := GetLocation(`{ "foo": "bar" }`, 4) // inside "foo"
	if loc.PreviousNode == nil {
		t.Fatal("PreviousNode = nil, want the property node")
	}
	if loc.PreviousNode.Type != NodeProperty {
		t.Errorf("PreviousNode.Type = %q, want %q", loc.PreviousNode.Type, NodeProperty)
	}
	if loc.PreviousNode.Literal != "foo" {
		t.Errorf("PreviousNode.Literal = %v, want %q", loc.PreviousNode.Literal, "foo")
	}
	if loc.PreviousNode.Offset != 2 || loc.PreviousNode.Length != 5 {
		t.Errorf("PreviousNode span = (%d, %d), want (2, 5)", loc.PreviousNode.Offset, loc.PreviousNode.Length)
	}
	if loc.PreviousNode.ColonOffset != -1 {
		t.Errorf("PreviousNode.ColonOffset = %d, want -1", loc.PreviousNode.ColonOffset)
	}
}

// TestLocationPreviousNodeClearedAtContainerStart pins the two clears that no
// other case reaches. A container beginning while PreviousNode is still live
// from a preceding element must clear it. These inputs are reachable because
// parseArray's CommaExpected recovery reports without skipping, so '{' or '['
// arrives with the previous literal still recorded. The '}' / ']' / ':' clears
// are covered by TestGetLocationProperties and TestGetLocationArrays; these
// two are not.
func TestLocationPreviousNodeClearedAtContainerStart(t *testing.T) {
	for _, c := range []struct {
		text string
		off  int
	}{
		{`[1 { "b": 2}]`, 4}, // object begins with the number 1 still live
		{`[1 [ 2]]`, 4},      // array begins with the number 1 still live
		{`[1 {} ]`, 4},       // empty object, same
	} {
		if got := GetLocation(c.text, c.off); got.PreviousNode != nil {
			t.Errorf("GetLocation(%q, %d).PreviousNode = %v, want nil",
				c.text, c.off, got.PreviousNode.Type)
		}
	}
}

func TestLocationMatches(t *testing.T) {
	loc := Location{Path: Path{"a", "b", 0}}
	tests := []struct {
		pattern Path
		want    bool
	}{
		{Path{"a", "b", 0}, true},
		{Path{"a", "*", 0}, true},
		{Path{"*", "*", 0}, true},
		{Path{"*", "*", "*"}, true},
		{Path{}, true},         // the empty pattern matches anything
		{Path{"a"}, true},      // prefix match
		{Path{"a", "b"}, true}, // prefix match
		{Path{"a", "c", 0}, false},
		{Path{"a", "b", 1}, false},   // wrong index, last position
		{Path{"a", "b", "0"}, false}, // a string never matches an int segment
		{Path{0}, false},             // an int never matches a string segment
		{Path{"b"}, false},
		{Path{"a", "b", 0, "x"}, false}, // longer than the path
		{Path{"a", "b", 0, "*"}, false}, // longer than the path, wildcard tail
	}
	for _, tc := range tests {
		if got := loc.Matches(tc.pattern); got != tc.want {
			t.Errorf("Matches(%#v) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

// TestLocationMatchesEmptyPath pins the zero Location: nothing but the empty
// pattern matches an empty path.
func TestLocationMatchesEmptyPath(t *testing.T) {
	var loc Location
	if !loc.Matches(Path{}) {
		t.Error("the empty pattern must match the empty path")
	}
	if loc.Matches(Path{"a"}) {
		t.Error("a non-empty pattern must not match the empty path")
	}
	if loc.Matches(Path{"*"}) {
		t.Error("a wildcard has nothing to match against in an empty path")
	}
}

func TestLocationMatchesMalformedPattern(t *testing.T) {
	loc := Location{Path: Path{"a"}}
	if loc.Matches(Path{1.5}) {
		t.Error("a malformed pattern element must not match")
	}
	// Malformed in a later position, after a prefix that does match.
	loc3 := Location{Path: Path{"a", "b", 0}}
	if loc3.Matches(Path{"a", 1.5}) {
		t.Error("a malformed pattern element must not match, even after a matching prefix")
	}
	if loc3.Matches(Path{"a", "b", int64(0)}) {
		t.Error("only int is a valid index segment; int64 is malformed")
	}
}

// TestLocationMatchesNilSegment covers the Go-only case that cannot arise
// upstream: a property whose key is not a string leaves a nil placeholder in
// the path, so nil can appear on either side. Matches must not panic, and a
// nil pattern element is malformed, so it never matches.
func TestLocationMatchesNilSegment(t *testing.T) {
	nilPath := Location{Path: Path{nil}}
	if nilPath.Matches(Path{nil}) {
		t.Error("a nil pattern element is malformed and must not match")
	}
	if !nilPath.Matches(Path{"*"}) {
		t.Error(`"*" must match any single segment, including a nil placeholder`)
	}
	if nilPath.Matches(Path{"a"}) {
		t.Error("a string pattern element must not match a nil segment")
	}
	if !nilPath.Matches(Path{}) {
		t.Error("the empty pattern must match a path of nil segments")
	}
	strPath := Location{Path: Path{"a"}}
	if strPath.Matches(Path{nil}) {
		t.Error("a nil pattern element is malformed and must not match a string segment")
	}
}

// TestLocationMatchesDoubleStar pins upstream's "**" branch. "**" never
// advances the pattern cursor itself -- it only permits skipping path
// segments -- so a pattern containing it matches only when a later path
// segment satisfies the very same pattern element literally.
func TestLocationMatchesDoubleStar(t *testing.T) {
	tests := []struct {
		path    Path
		pattern Path
		want    bool
	}{
		// The skip branch: "**" lets "x" be passed over, then the literal
		// segment "**" satisfies the pattern element.
		{Path{"x", "**", "b"}, Path{"**", "b"}, true},
		// Without a literal "**" to land on, the pattern never completes.
		{Path{"a", "b"}, Path{"a", "**"}, false},
		{Path{"x", "y"}, Path{"**"}, false},
		{Path{"x", "a"}, Path{"**", "a"}, false},
		// A literal "**" segment matches a literal "**" pattern element.
		{Path{"**"}, Path{"**"}, true},
	}
	for _, tc := range tests {
		loc := Location{Path: tc.path}
		if got := loc.Matches(tc.pattern); got != tc.want {
			t.Errorf("Location{%#v}.Matches(%#v) = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
}

// TestLocationMatchesFromGetLocation is the port of upstream's
// "location: matches".
func TestLocationMatchesFromGetLocation(t *testing.T) {
	assertMatchesLocation(t, `{ "dependencies": { | } }`, Path{"dependencies"}, true)
	assertMatchesLocation(t, `{ "dependencies": { "fo| } }`, Path{"dependencies"}, true)
	assertMatchesLocation(t, `{ "dependencies": { "fo|" } }`, Path{"dependencies"}, true)
	assertMatchesLocation(t, `{ "dependencies": { "fo|": 1 } }`, Path{"dependencies"}, true)
	assertMatchesLocation(t, `{ "dependencies": { "fo": | } }`, Path{"dependencies", "*"}, true)
	// Negative controls, so the helper cannot pass by always returning true.
	assertMatchesLocation(t, `{ "dependencies": { "fo|": 1 } }`, Path{"devDependencies"}, false)
	assertMatchesLocation(t, `{ "dependencies": { "fo": | } }`, Path{"dependencies", "*", "*"}, false)
}
