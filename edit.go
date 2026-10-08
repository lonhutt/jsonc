package jsonc

import (
	"fmt"
	"slices"
	"strings"
)

// Edit replaces Length bytes at Offset with Content. An Edit with Length 0 is
// an insertion; one with empty Content is a deletion.
type Edit struct {
	Offset, Length int
	Content        string
}

// ApplyEdits applies edits to text and returns the result.
//
// Edits may be supplied in any order; offsets all refer to the original text.
// Adjacent edits are fine. Overlapping edits, or an edit outside the text, are
// an error rather than a silent last-write-wins.
//
// Edits at the same offset resolve as node-jsonc-parser does: insertions keep
// their input order, and an insertion goes before a replacement that starts
// where it does.
func ApplyEdits(text string, edits []Edit) (string, error) {
	if len(edits) == 0 {
		return text, nil
	}

	// Sort a copy, so the caller's slice is not reordered. Stable, so that
	// insertions at one offset keep their order; shorter first, so that an
	// insertion precedes a replacement at its offset.
	sorted := slices.Clone(edits)
	slices.SortStableFunc(sorted, func(a, b Edit) int {
		if a.Offset != b.Offset {
			return a.Offset - b.Offset
		}
		return a.Length - b.Length
	})

	size, prevEnd := len(text), 0
	for i, e := range sorted {
		// Length is compared against what remains rather than summed with
		// Offset, which could overflow. An Offset past the end leaves a
		// negative remainder, so this rejects that too.
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
