package jsonc

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Edit replaces Length bytes at Offset with Content.
type Edit struct {
	Offset, Length int
	Content        string
}

// ApplyEdits applies edits to text, in any order; offsets all refer to the
// original text. Overlapping or out-of-bounds edits are an error, not
// last-write-wins. At one offset, insertions keep their order and go before a
// replacement, as upstream.
func ApplyEdits(text string, edits []Edit) (string, error) {
	if len(edits) == 0 {
		return text, nil
	}

	// Stable, and shorter first, for the tie rules above. Sort a copy; the
	// caller's slice stays as it was.
	sorted := slices.Clone(edits)
	slices.SortStableFunc(sorted, func(a, b Edit) int {
		if a.Offset != b.Offset {
			return a.Offset - b.Offset
		}
		return a.Length - b.Length
	})

	size, prevEnd := len(text), 0
	for i, e := range sorted {
		// Compare against the remainder; Offset+Length can overflow. A negative
		// remainder (Offset past the end) is rejected too.
		if e.Offset < 0 || e.Length < 0 || e.Length > len(text)-e.Offset {
			return "", fmt.Errorf("jsonc: edit %+v is out of bounds for a document of length %d", e, len(text))
		}
		if e.Offset < prevEnd {
			return "", fmt.Errorf("jsonc: edits overlap: %+v and %+v", sorted[i-1], e)
		}
		prevEnd = e.Offset + e.Length
		size += len(e.Content) - e.Length
	}

	var b strings.Builder
	b.Grow(size)
	last := 0
	for _, e := range sorted {
		b.WriteString(text[last:e.Offset])
		b.WriteString(e.Content)
		last = e.Offset + e.Length
	}
	b.WriteString(text[last:])
	return b.String(), nil
}

// ModifyOptions controls Modify.
type ModifyOptions struct {
	// Format, when set, formats the lines a change touches; nil leaves the
	// inserted text compact.
	Format *FormatOptions
	// IsArrayInsertion makes an int path element insert before that index
	// instead of replacing it.
	IsArrayInsertion bool
	// GetInsertionIndex picks where a new property goes, given the existing
	// names in order; nil appends. Clamped to the valid range.
	GetInsertionIndex func(properties []string) int
}

// Modify returns the edit that sets the value at path, creating missing
// parents along the way. An empty path replaces the whole document. An index
// of -1, or past the end, appends to an array.
//
// value is encoded with encoding/json; nil is JSON null, not a deletion.
func Modify(text string, path Path, value any, o ModifyOptions) ([]Edit, error) {
	return setProperty(text, path, value, false, o)
}

// Remove returns the edit that deletes the value at path, with one adjacent
// comma. Comments inside the removed span go too, as upstream. A missing
// property is no edit; a missing parent or array element is an error.
func Remove(text string, path Path, o ModifyOptions) ([]Edit, error) {
	return setProperty(text, path, nil, true, o)
}

// setProperty is upstream's setProperty; remove stands in for its undefined.
func setProperty(text string, path Path, value any, remove bool, o ModifyOptions) ([]Edit, error) {
	for i, seg := range path {
		switch seg := seg.(type) {
		case string:
		case int:
			// checked here too, so a missing array doesn't let it through
			if seg < -1 {
				return nil, fmt.Errorf("jsonc: path element %d is %d; an index can't be below -1", i, seg)
			}
		default:
			return nil, fmt.Errorf("jsonc: path element %d is a %T; must be string or int", i, seg)
		}
	}

	root, _ := ParseTree(text)
	var parent *Node
	var last any
	for rest := path; len(rest) > 0; {
		last, rest = rest[len(rest)-1], rest[:len(rest)-1]
		if parent = FindNodeAtLocation(root, rest); parent != nil || remove {
			break
		}
		if key, ok := last.(string); ok {
			value = map[string]any{key: value}
		} else {
			value = []any{value}
		}
	}

	if parent == nil {
		if remove {
			return nil, fmt.Errorf("jsonc: nothing to remove at %v", path)
		}
		content, err := marshal(value)
		if err != nil {
			return nil, err
		}
		e := Edit{Content: content}
		if root != nil {
			e.Offset, e.Length = root.Offset, root.Length
		}
		return withFormatting(text, e, o)
	}

	key, isKey := last.(string)
	switch {
	case parent.Type == NodeObject && isKey:
		return setObjectProperty(text, parent, key, value, remove, o)
	case parent.Type == NodeArray && !isKey:
		return setArrayElement(text, parent, last.(int), value, remove, o)
	case isKey:
		return nil, fmt.Errorf("jsonc: can't set property %q on a %s", key, parent.Type)
	default:
		return nil, fmt.Errorf("jsonc: can't set index %d on a %s", last, parent.Type)
	}
}

func setObjectProperty(text string, parent *Node, key string, value any, remove bool, o ModifyOptions) ([]Edit, error) {
	children := parent.Children
	existing := FindNodeAtLocation(parent, Path{key})
	if remove {
		if existing == nil {
			return nil, nil
		}
		i := slices.Index(children, existing.Parent)
		begin, end := parent.Offset+1, existing.Parent.Offset+existing.Parent.Length
		if i > 0 {
			prev := children[i-1]
			begin = prev.Offset + prev.Length
		} else if len(children) > 1 {
			end = children[1].Offset
		}
		return withFormatting(text, Edit{Offset: begin, Length: end - begin}, o)
	}

	content, err := marshal(value)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return withFormatting(text, Edit{Offset: existing.Offset, Length: existing.Length, Content: content}, o)
	}

	encodedKey, err := marshal(key)
	if err != nil {
		return nil, err
	}
	property := encodedKey + ": " + content

	index := len(children)
	if o.GetInsertionIndex != nil {
		names := make([]string, len(children))
		for i, p := range children {
			names[i], _ = p.Children[0].Literal.(string)
		}
		index = clamp(o.GetInsertionIndex(names), 0, len(children))
	}

	var e Edit
	switch {
	case index > 0:
		prev := children[index-1]
		e = Edit{Offset: prev.Offset + prev.Length, Content: "," + property}
	case len(children) == 0:
		e = Edit{Offset: parent.Offset + 1, Content: property}
	default:
		e = Edit{Offset: parent.Offset + 1, Content: property + ","}
	}
	return withFormatting(text, e, o)
}

func setArrayElement(text string, parent *Node, index int, value any, remove bool, o ModifyOptions) ([]Edit, error) {
	children := parent.Children
	n := len(children)

	if remove {
		// Upstream crashes, empties the array, or inserts "undefined" here.
		if index < 0 || index >= n {
			return nil, fmt.Errorf("jsonc: no element %d to remove from an array of %d", index, n)
		}
		var e Edit
		switch {
		case n == 1:
			e = Edit{Offset: parent.Offset + 1, Length: parent.Length - 2}
		case index == n-1:
			// Upstream stops 2 bytes before the array's end, assuming "\n]";
			// that keeps the last digit of `[1,2]` and cuts into a comment
			// before the ']'.
			prev, last := children[index-1], children[index]
			begin, lastEnd := prev.Offset+prev.Length, last.Offset+last.Length
			end := max(tokenBoundary(text, lastEnd, parent.Offset+parent.Length-2), lastEnd)
			e = Edit{Offset: begin, Length: end - begin}
		default:
			e = Edit{Offset: children[index].Offset, Length: children[index+1].Offset - children[index].Offset}
		}
		return withFormatting(text, e, o)
	}

	if index < -1 {
		return nil, fmt.Errorf("jsonc: can't set element %d of an array", index)
	}
	content, err := marshal(value)
	if err != nil {
		return nil, err
	}
	var e Edit
	switch {
	case index == -1 && n == 0:
		e = Edit{Offset: parent.Offset + 1, Content: content}
	case index == -1:
		prev := children[n-1]
		e = Edit{Offset: prev.Offset + prev.Length, Content: "," + content}
	case !o.IsArrayInsertion && index < n:
		e = Edit{Offset: children[index].Offset, Length: children[index].Length, Content: content}
	case n == 0:
		e = Edit{Offset: parent.Offset + 1, Content: content}
	case index == 0:
		e = Edit{Offset: parent.Offset + 1, Content: content + ","}
	default:
		prev := children[min(index, n)-1]
		e = Edit{Offset: prev.Offset + prev.Length, Content: "," + content}
	}
	return withFormatting(text, e, o)
}

// tokenBoundary returns at, or the start of the token it falls inside,
// scanning from from.
func tokenBoundary(text string, from, at int) int {
	s := NewScanner(text)
	s.SetPosition(from)
	for k := s.Scan(); k != EOF && s.Offset() < at; k = s.Scan() {
		if s.Offset()+s.Length() > at {
			return s.Offset()
		}
	}
	return at
}

// marshal is JSON.stringify as near as encoding/json gets; HTML escaping off.
func marshal(v any) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("jsonc: can't encode value: %w", err)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// withFormatting applies e, formats the lines it touched, and returns one edit
// against text covering both; upstream's withFormatting.
func withFormatting(text string, e Edit, o ModifyOptions) ([]Edit, error) {
	if o.Format == nil {
		return []Edit{e}, nil
	}
	newText := text[:e.Offset] + e.Content + text[e.Offset+e.Length:]

	begin, end := e.Offset, e.Offset+len(e.Content)
	if e.Length == 0 || e.Content == "" {
		for begin > 0 && !isLineBreak(newText[begin-1]) {
			begin--
		}
		for end < len(newText) && !isLineBreak(newText[end]) {
			end++
		}
	}

	fo := *o.Format
	fo.KeepLines = false
	// Format's edits all fall inside [begin, end): the range starts at a line or
	// token start and its scan stops at the line end. Upstream also widens
	// begin and end per edit; that never fires.
	edits := Format(newText, &Range{Offset: begin, Length: end - begin}, fo)
	newText, err := ApplyEdits(newText, edits)
	if err != nil {
		return nil, err
	}
	for _, f := range edits {
		end += len(f.Content) - f.Length
	}
	return []Edit{{Offset: begin, Length: len(text) - (len(newText) - end) - begin, Content: newText[begin:end]}}, nil
}
