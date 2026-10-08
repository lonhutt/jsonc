package jsonc

import (
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
