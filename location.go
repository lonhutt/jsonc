package jsonc

// Path identifies a value in a document; each element is a property name
// (string) or an array index (int). Any other element is malformed: Modify and
// Remove return an error, FindNodeAtLocation returns nil, and Location.Matches
// returns false.
type Path []any

// Location is where an offset sits in a document.
type Location struct {
	// PreviousNode is the token the offset is in or just after: a key being
	// typed, or a literal just finished. Nil right after ':' or ',', or next to
	// a bracket.
	//
	// It's a lone node, not part of a tree; Parent and Children are nil. For a
	// property, Literal is the key and the span is just the key token.
	PreviousNode *Node
	// Path is the path at the offset. On a key, the last element is the key
	// name; "" if nothing's typed yet. Never nil from GetLocation.
	Path            Path
	IsAtPropertyKey bool
}

// Matches reports whether pattern describes the location's path. "*" matches
// any one segment, and a shorter pattern matches as a prefix, so
// Path{"dependencies"} matches anywhere inside that object. An element that
// isn't a string or int never matches.
//
// "**" is only here for parity with upstream, where it isn't a recursive
// wildcard; it skips segments but never advances, so it only matches a later
// segment that's literally "**".
func (l Location) Matches(pattern Path) bool {
	k := 0
	for i := 0; k < len(pattern) && i < len(l.Path); i++ {
		if segmentEqual(pattern[k], l.Path[i]) || pattern[k] == "*" {
			k++
		} else if pattern[k] != "**" {
			return false
		}
	}
	return k == len(pattern)
}

// segmentEqual compares by type instead of ==, which would make the nil left by
// a non-string key equal nil, and panic on an uncomparable element.
func segmentEqual(pattern, segment any) bool {
	switch p := pattern.(type) {
	case string:
		s, ok := segment.(string)
		return ok && s == p
	case int:
		s, ok := segment.(int)
		return ok && s == p
	default:
		return false
	}
}

// GetLocation returns where offset sits in text. It's meant for completion in
// half-typed files, so it never reports errors.
func GetLocation(text string, offset int) Location {
	// segments tracks the caret, which isn't the same as Span.Path; after a ','
	// the caret is already on the next element.
	segments := Path{}
	var previousNode *Node
	isAtPropertyKey := false

	// Visit can't be stopped from a callback (upstream throws), so events past
	// the caret are ignored.
	done := false

	setPreviousNode := func(value any, offset, length int, t NodeType) {
		// upstream also tracks colonOffset here; it's never observable
		previousNode = &Node{Type: t, Literal: value, Offset: offset, Length: length, ColonOffset: -1}
	}

	Visit(text, Visitor{
		OnObjectBegin: func(s Span) bool {
			if done {
				return true
			}
			if offset <= s.Offset {
				done = true
				return true
			}
			previousNode = nil
			isAtPropertyKey = true
			segments = append(segments, "") // first key, not typed yet
			return true
		},
		OnObjectProperty: func(name string, s Span) {
			if done {
				return
			}
			if offset < s.Offset {
				done = true
				return
			}
			setPreviousNode(name, s.Offset, s.Length, NodeProperty)
			segments[len(segments)-1] = name
			// a caret right at the end of a key is still on it
			if offset <= s.Offset+s.Length {
				done = true
			}
		},
		OnObjectEnd: func(s Span) {
			if done {
				return
			}
			if offset <= s.Offset {
				done = true
				return
			}
			previousNode = nil
			segments = segments[:len(segments)-1]
		},
		OnArrayBegin: func(s Span) bool {
			if done {
				return true
			}
			if offset <= s.Offset {
				done = true
				return true
			}
			previousNode = nil
			segments = append(segments, 0)
			return true
		},
		OnArrayEnd: func(s Span) {
			if done {
				return
			}
			if offset <= s.Offset {
				done = true
				return
			}
			previousNode = nil
			segments = segments[:len(segments)-1]
		},
		OnLiteralValue: func(v any, s Span) {
			if done {
				return
			}
			if offset < s.Offset {
				done = true
				return
			}
			setPreviousNode(v, s.Offset, s.Length, literalType(v))
			if offset <= s.Offset+s.Length {
				done = true
			}
		},
		OnSeparator: func(sep string, s Span) {
			if done {
				return
			}
			if offset <= s.Offset {
				done = true
				return
			}
			switch {
			case sep == ":" && previousNode != nil && previousNode.Type == NodeProperty:
				// the key's done; the caret is at a value now
				isAtPropertyKey = false
				previousNode = nil
			case sep == ",":
				// next element: the next index, or a new, untyped key
				if i, ok := segments[len(segments)-1].(int); ok {
					segments[len(segments)-1] = i + 1
				} else {
					isAtPropertyKey = true
					segments[len(segments)-1] = ""
				}
				previousNode = nil
			}
		},
	})

	return Location{
		PreviousNode:    previousNode,
		Path:            segments,
		IsAtPropertyKey: isAtPropertyKey,
	}
}
