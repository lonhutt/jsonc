package jsonc

// NodeType is the kind of a Node in the parse tree.
type NodeType string

const (
	NodeObject   NodeType = "object"
	NodeArray    NodeType = "array"
	NodeProperty NodeType = "property"
	NodeString   NodeType = "string"
	NodeNumber   NodeType = "number"
	NodeBoolean  NodeType = "boolean"
	NodeNull     NodeType = "null"
)

// Node is one node of a parsed JSONC document. Offsets and lengths are byte
// offsets into the original text.
//
// A Node tree is immutable once ParseTree returns and is safe for concurrent
// reads, despite the Parent pointers.
//
// A property node has exactly two children when well formed: the key (a
// NodeString) and the value. Its ColonOffset locates the ':' between them.
type Node struct {
	Type NodeType
	// Literal is the value of a scalar node (string, number, boolean, null).
	// It is nil for object, array, and property nodes; use Value for those.
	Literal        any
	Offset, Length int
	// ColonOffset is the byte offset of the ':' in a property node, or -1.
	ColonOffset int
	Parent      *Node
	Children    []*Node
}

// Value materializes this subtree as a Go value, using the same types as
// Parse. It replaces upstream's getNodeValue.
func (n *Node) Value() any {
	if n == nil {
		return nil
	}
	switch n.Type {
	case NodeObject:
		m := make(map[string]any, len(n.Children))
		for _, prop := range n.Children {
			if len(prop.Children) != 2 {
				continue // malformed property from a tolerant parse
			}
			key, ok := prop.Children[0].Literal.(string)
			if !ok {
				continue
			}
			m[key] = prop.Children[1].Value()
		}
		return m
	case NodeArray:
		out := make([]any, 0, len(n.Children))
		for _, c := range n.Children {
			out = append(out, c.Value())
		}
		return out
	case NodeProperty:
		if len(n.Children) == 2 {
			return n.Children[1].Value()
		}
		return nil
	default:
		return n.Literal
	}
}

// Path returns the JSON path from the document root to this node. It replaces
// upstream's getNodePath.
func (n *Node) Path() Path {
	if n == nil || n.Parent == nil {
		return Path{}
	}
	parentPath := n.Parent.Path()
	switch n.Parent.Type {
	case NodeProperty:
		// The key node has the same path as the property itself.
		key, _ := n.Parent.Children[0].Literal.(string)
		return append(parentPath, key)
	case NodeArray:
		for i, c := range n.Parent.Children {
			if c == n {
				return append(parentPath, i)
			}
		}
	}
	return parentPath
}

// ParseTree parses text into a tree that preserves source offsets and object
// key order. Like Parse it is fault tolerant, returning a best-effort tree
// alongside every problem found. It returns nil only when the document
// contains no value at all.
func ParseTree(text string, opts ...ParseOption) (*Node, []ParseError) {
	var (
		root    *Node
		current *Node
		errs    []ParseError
	)

	// ensureProperty closes a property that has received BOTH its key and its
	// value, extending it to cover the value. A property's parent is always an
	// object, so one pop ends it.
	ensureProperty := func() {
		if current != nil && current.Type == NodeProperty && len(current.Children) == 2 {
			current.Length = current.Children[1].Offset + current.Children[1].Length - current.Offset
			current = current.Parent
		}
	}

	// ensurePropertyComplete closes a property that will NEVER receive a
	// value, extending it to endOffset. It does not require a value child.
	ensurePropertyComplete := func(endOffset int) {
		if current != nil && current.Type == NodeProperty {
			current.Length = endOffset - current.Offset
			current = current.Parent
		}
	}

	// attach adds n as the last child of current, or makes it the root.
	attach := func(n *Node) *Node {
		if current == nil {
			root = n
			return n
		}
		n.Parent = current
		current.Children = append(current.Children, n)
		return n
	}

	newNode := func(t NodeType, s Span) *Node {
		return &Node{Type: t, Offset: s.Offset, Length: s.Length, ColonOffset: -1}
	}

	Visit(text, Visitor{
		OnObjectBegin: func(s Span) {
			current = attach(newNode(NodeObject, s))
		},
		OnObjectProperty: func(name string, s Span) {
			// Defensive: close any property still open before attaching a
			// sibling. Unreachable under the current grammar -- every recovery
			// skip in Visit stops at ',' or '}' or EOF, so a property name
			// cannot appear while a prior property is still open. Kept because
			// the cost is one no-op branch and the cost of being wrong is
			// silent tree corruption, which is what this task just fixed.
			ensurePropertyComplete(s.Offset)
			prop := attach(newNode(NodeProperty, s))
			current = prop
			key := newNode(NodeString, s)
			key.Literal = name
			key.Parent = prop
			prop.Children = append(prop.Children, key)
		},
		OnObjectEnd: func(s Span) {
			ensurePropertyComplete(s.Offset)
			// s is positioned at the closing brace (or EOF for an
			// unterminated object); Length covers it.
			current.Length = s.Offset + s.Length - current.Offset
			current = current.Parent
			ensureProperty()
		},
		OnArrayBegin: func(s Span) {
			current = attach(newNode(NodeArray, s))
		},
		OnArrayEnd: func(s Span) {
			current.Length = s.Offset + s.Length - current.Offset
			current = current.Parent
			ensureProperty()
		},
		OnLiteralValue: func(v any, s Span) {
			n := newNode(literalType(v), s)
			n.Literal = v
			attach(n)
			ensureProperty()
		},
		OnSeparator: func(sep string, s Span) {
			if sep == ":" && current != nil && current.Type == NodeProperty {
				current.ColonOffset = s.Offset
			}
			if sep == "," {
				ensurePropertyComplete(s.Offset)
			}
		},
		OnError: func(c ParseErrorCode, s Span) {
			errs = append(errs, ParseError{
				Code: c, Offset: s.Offset, Length: s.Length,
				StartLine: s.StartLine, StartCol: s.StartCol,
			})
		},
	}, opts...)

	return root, errs
}

func literalType(v any) NodeType {
	switch v.(type) {
	case nil:
		return NodeNull
	case bool:
		return NodeBoolean
	case string:
		return NodeString
	default:
		// float64 or json.Number
		return NodeNumber
	}
}

// FindNodeAtLocation returns the node at path, or nil if there is none.
//
// A malformed path element -- anything other than a string or an int --
// yields nil, the same as a path that simply does not resolve.
func FindNodeAtLocation(root *Node, path Path) *Node {
	node := root
	for _, seg := range path {
		if node == nil {
			return nil
		}
		switch key := seg.(type) {
		case string:
			if node.Type != NodeObject {
				return nil
			}
			var found *Node
			for _, prop := range node.Children {
				if len(prop.Children) == 2 {
					if k, ok := prop.Children[0].Literal.(string); ok && k == key {
						found = prop.Children[1]
						break
					}
				}
			}
			if found == nil {
				return nil
			}
			node = found
		case int:
			if node.Type != NodeArray || key < 0 || key >= len(node.Children) {
				return nil
			}
			node = node.Children[key]
		default:
			return nil
		}
	}
	return node
}

// FindNodeAtOffset returns the innermost node containing offset, or nil.
//
// With includeRightBound false a node matches offsets in
// [Offset, Offset+Length); with it true the range is inclusive at both ends,
// which is what an editor wants when the caret sits just past a token.
func FindNodeAtOffset(root *Node, offset int, includeRightBound bool) *Node {
	if root == nil {
		return nil
	}
	if !nodeContains(root, offset, includeRightBound) {
		return nil
	}
	node := root
	for {
		var next *Node
		for _, c := range node.Children {
			if nodeContains(c, offset, includeRightBound) {
				next = c
				break
			}
		}
		if next == nil {
			return node
		}
		node = next
	}
}

func nodeContains(n *Node, offset int, includeRightBound bool) bool {
	if offset < n.Offset {
		return false
	}
	end := n.Offset + n.Length
	if includeRightBound {
		return offset <= end
	}
	return offset < end
}
