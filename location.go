package jsonc

// Path identifies a value inside a JSON document. Each element is either a
// string (an object property name) or an int (an array index).
//
// A Path element of any other type is malformed. How that is reported depends
// on the function: Modify and Remove return an error, FindNodeAtLocation
// returns nil, and Location.Matches returns false.
type Path []any

// Location describes the position of an offset inside a JSON document: which
// value encloses it, and whether the offset sits on a property name.
type Location struct {
	// PreviousNode is the node the offset falls inside or immediately after:
	// the property whose key is being typed, or the literal just closed. It is
	// nil when the offset sits somewhere with no such node -- right after a
	// ':' or ',', or just inside or after a bracket.
	//
	// It is a standalone node describing one token, not part of a parse tree:
	// Parent and Children are always nil, and only Type, Literal, Offset and
	// Length carry meaning. For a NodeProperty, Literal is the key name and
	// the span covers the key token alone. Use ParseTree when a real tree is
	// what you want.
	PreviousNode *Node
	// Path is the JSON path of the value at the offset. For a property key,
	// the last element is the key name -- the empty string when no key has
	// been typed yet. GetLocation never returns a nil Path, though the zero
	// Location has one.
	Path Path
	// IsAtPropertyKey reports whether the offset is on a property name rather
	// than a value.
	IsAtPropertyKey bool
}

// Matches reports whether pattern describes this location's path.
//
// A pattern element of "*" matches any single segment, and a pattern shorter
// than the path matches as a prefix, so Matches(Path{"dependencies"}) is true
// anywhere inside the "dependencies" object. The empty pattern matches every
// location; a pattern longer than the path matches none.
//
// A malformed pattern element -- anything other than a string or an int --
// never matches.
//
// The element "**" is carried over from upstream, where it permits skipping
// path segments but never advances the pattern itself. The practical effect
// is that a pattern containing "**" matches only if some later path segment
// is the literal string "**"; it is not a recursive wildcard. It is kept for
// behavioral parity, not because it is useful.
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

// segmentEqual reports whether a pattern element and a path segment are the
// same segment. Only a string or an int is a well formed segment, so anything
// else -- including the nil placeholder a non-string property key leaves in a
// path -- is never equal to anything. Comparing the interfaces directly would
// instead make nil equal nil, and would panic on an uncomparable element.
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

// GetLocation evaluates the location of offset within text: the path of the
// value there, the node just before it, and whether it is a property key.
//
// It is defined for incomplete and invalid documents -- supporting completion
// in a half-typed file is its main purpose -- so it never reports errors.
func GetLocation(text string, offset int) Location {
	// segments is the live path stack, maintained here rather than taken from
	// Span.Path: this stack tracks where the *caret* is, which is not the same
	// thing. After a ',' the caret is at the next, not-yet-parsed element, so
	// the stack advances an array index or resets an object key to "" --
	// neither of which the parser's own path does.
	segments := Path{}
	var previousNode *Node
	isAtPropertyKey := false

	// done replaces upstream's earlyReturnException. Visit cannot be stopped
	// from a callback, so every callback returns immediately once the caret
	// has been passed; the remaining events simply have no effect.
	done := false

	setPreviousNode := func(value any, offset, length int, t NodeType) {
		// Upstream also clears colonOffset here and sets it from the ':'
		// separator below. Both are unobservable: the separator handler drops
		// previousNode in the same breath, and this node is freshly allocated
		// rather than the single reused instance upstream keeps.
		previousNode = &Node{Type: t, Literal: value, Offset: offset, Length: length, ColonOffset: -1}
	}

	Visit(text, Visitor{
		OnObjectBegin: func(s Span) {
			if done {
				return
			}
			if offset <= s.Offset {
				done = true
				return
			}
			previousNode = nil
			isAtPropertyKey = true
			segments = append(segments, "") // a placeholder for the first key
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
			// The right bound is included: a caret at the very end of a key is
			// still on the key.
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
		OnArrayBegin: func(s Span) {
			if done {
				return
			}
			if offset <= s.Offset {
				done = true
				return
			}
			previousNode = nil
			segments = append(segments, 0)
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
				// The key is finished; the caret is now at a value.
				isAtPropertyKey = false
				previousNode = nil
			case sep == ",":
				// Step to the next element: the next array index, or a fresh,
				// not-yet-typed object key.
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
