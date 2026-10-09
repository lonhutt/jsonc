package jsonc

import (
	"encoding/json"
	"reflect"
	"testing"
	"unicode/utf8"
)

var fuzzSeeds = []string{
	`{}`, `[]`, `{"a":1}`, `[1,2,3]`, `{"a":[1,{"b":null}]}`,
	"{\n  // c\n  \"a\": 1\n}", `{"a":1,}`, `{"a"`, `[`, ``,
	`"😀"`, `{"é":"ü"}`, `/* c */{}`, `{"a":1e999}`,
	"{\r\n\t\"a\": [1, 2]\r\n}", `{"a":}`, `[1 2]`, "\n\n  {\"漢字\": \"😀\"}  \n",
}

func FuzzScanner(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		s := NewScanner(text)
		end := 0
		for k := s.Scan(); k != EOF; k = s.Scan() {
			if s.Offset() != end || s.Length() <= 0 || s.Offset()+s.Length() > len(text) {
				t.Fatalf("%v at %d+%d after %d: tokens have to tile the %d-byte input", k, s.Offset(), s.Length(), end, len(text))
			}
			if s.StartLine() < 0 || s.StartColumn() < 0 {
				t.Fatalf("%v at %d: line %d, column %d", k, s.Offset(), s.StartLine(), s.StartColumn())
			}
			end = s.Offset() + s.Length()
		}
		if end != len(text) {
			t.Fatalf("tokens covered %d of %d bytes", end, len(text))
		}
	})
}

func FuzzParse(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		v, errs := Parse(text)
		// GetLocation stops early with a panic it recovers; no other may escape
		GetLocation(text, len(text)/2)
		for _, e := range errs {
			if e.Offset < 0 || e.Length < 0 || e.Offset+e.Length > len(text) || e.StartLine < 0 || e.StartCol < 0 {
				t.Fatalf("error out of bounds for %d bytes: %+v", len(text), e)
			}
		}
		// A property never opens while another is still open, since Visit's
		// recovery always stops at ',', '}' or EOF; if it did, the new one
		// would land inside the old one.
		root, _ := ParseTree(text)
		var check func(n *Node)
		check = func(n *Node) {
			if n.Type == NodeProperty && (len(n.Children) == 0 || len(n.Children) > 2 || n.Children[0].Type != NodeString) {
				t.Fatalf("ParseTree(%q): property at %d has children %v", text, n.Offset, n.Children)
			}
			for _, c := range n.Children {
				if c.Offset < n.Offset || c.Offset+c.Length > n.Offset+n.Length {
					t.Fatalf("ParseTree(%q): %v at %d+%d outside its parent at %d+%d", text, c.Type, c.Offset, c.Length, n.Offset, n.Length)
				}
				check(c)
			}
		}
		if root != nil {
			check(root)
		}

		// encoding/json replaces invalid UTF-8 where we keep the bytes, so only
		// compare valid text.
		var want any
		if len(errs) == 0 && utf8.ValidString(text) && json.Unmarshal([]byte(text), &want) == nil && !reflect.DeepEqual(v, want) {
			t.Fatalf("Parse(%q) = %#v, encoding/json = %#v", text, v, want)
		}
	})
}

func FuzzFormatIdempotent(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, 1, 4)
	}
	f.Fuzz(func(t *testing.T, text string, offset, length int) {
		o := FormatOptions{TabSize: 2, InsertSpaces: true, EOL: "\n"}

		// Any range, even one splitting a rune, only rewrites whitespace.
		r := Range{Offset: offset, Length: length}
		edits := Format(text, &r, o)
		checkEditInvariants(t, text, edits)
		got, err := ApplyEdits(text, edits)
		if err != nil {
			t.Fatalf("range %+v: %v", r, err)
		}
		if before, after := tokenSignature(text), tokenSignature(got); !reflect.DeepEqual(before, after) {
			t.Fatalf("range %+v changed tokens: %q -> %q", r, text, got)
		}

		before, errs := Parse(text)
		if len(errs) != 0 {
			return
		}
		once, err := ApplyEdits(text, Format(text, nil, o))
		if err != nil {
			t.Fatal(err)
		}
		after, errs := Parse(once)
		if len(errs) != 0 || !reflect.DeepEqual(before, after) {
			t.Fatalf("formatting %q gave %q: value %#v -> %#v, %v", text, once, before, after, errs)
		}
		if again := Format(once, nil, o); len(again) != 0 {
			t.Fatalf("formatting %q twice wasn't idempotent: %+v", text, again)
		}
	})
}

func FuzzModifyApply(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, "a")
		f.Add(s, "漢")
	}
	f.Fuzz(func(t *testing.T, text, key string) {
		// encoding/json rewrites invalid UTF-8 in a key, and with a duplicate
		// key Parse and Modify see different members.
		if _, errs := Parse(text); len(errs) != 0 || !utf8.ValidString(key) || hasDuplicateKeys(text) {
			return
		}
		root, _ := ParseTree(text)
		if root == nil || root.Type != NodeObject {
			return
		}

		o := ModifyOptions{Format: &FormatOptions{TabSize: 2, InsertSpaces: true, EOL: "\n"}}
		edits, err := Modify(text, Path{key}, 42, o)
		if err != nil {
			t.Fatalf("Modify(%q, %q): %v", text, key, err)
		}
		set, err := ApplyEdits(text, edits)
		if err != nil {
			t.Fatal(err)
		}
		v, errs := Parse(set)
		if m, ok := v.(map[string]any); len(errs) != 0 || !ok || m[key] != float64(42) {
			t.Fatalf("Modify(%q, %q) gave %q: %#v, %v", text, key, set, v, errs)
		}

		edits, err = Remove(set, Path{key}, o)
		if err != nil {
			t.Fatalf("Remove(%q, %q): %v", set, key, err)
		}
		removed, err := ApplyEdits(set, edits)
		if err != nil {
			t.Fatal(err)
		}
		v, errs = Parse(removed)
		if m, ok := v.(map[string]any); len(errs) != 0 || !ok || m[key] != nil {
			t.Fatalf("Remove(%q, %q) gave %q: %#v, %v", set, key, removed, v, errs)
		}
	})
}
