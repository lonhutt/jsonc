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

// Node is one node of the parse tree. Offsets are bytes. A tree doesn't change
// after ParseTree returns, so concurrent reads are safe.
//
// A well-formed property has two children, key then value. ColonOffset is a
// property's ':', or -1 if it has none; one with a ':' can still lack a value.
type Node struct {
	Type NodeType
	// Literal is a scalar's value; nil for objects, arrays and properties, which
	// have Value instead.
	Literal        any
	Offset, Length int
	ColonOffset    int
	Parent         *Node
	Children       []*Node
}

// Value returns the subtree as Parse would; upstream's getNodeValue.
func (n *Node) Value() any {
	if n == nil {
		return nil
	}
	switch n.Type {
	case NodeObject:
		m := make(map[string]any, len(n.Children))
		for _, prop := range n.Children {
			if len(prop.Children) != 2 {
				continue // incomplete property
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

// Path returns the path from the root to n; upstream's getNodePath.
func (n *Node) Path() Path {
	if n == nil || n.Parent == nil {
		return Path{}
	}
	parentPath := n.Parent.Path()
	switch n.Parent.Type {
	case NodeProperty:
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

// ParseTree parses text into a tree that keeps offsets and key order. It's
// fault tolerant like Parse; the root is nil only when there's no value at all.
func ParseTree(text string, opts ...ParseOption) (*Node, []ParseError) {
	var (
		root    *Node
		current *Node
		errs    []ParseError
	)

	// ensureProperty closes a property once it has its value. Its parent is
	// always an object, so one step up is enough.
	ensureProperty := func() {
		if current != nil && current.Type == NodeProperty && len(current.Children) == 2 {
			current.Length = current.Children[1].Offset + current.Children[1].Length - current.Offset
			current = current.Parent
		}
	}

	// ensurePropertyComplete closes a property that won't get a value, like
	// `{"a":}`, ending it at endOffset.
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
		OnObjectBegin: func(s Span) bool {
			current = attach(newNode(NodeObject, s))
			return true
		},
		OnObjectProperty: func(name string, s Span) {
			prop := attach(newNode(NodeProperty, s))
			current = prop
			key := newNode(NodeString, s)
			key.Literal = name
			key.Parent = prop
			prop.Children = append(prop.Children, key)
		},
		OnObjectEnd: func(s Span) {
			ensurePropertyComplete(s.Offset)
			// s is the closing brace, or EOF if unterminated
			current.Length = s.Offset + s.Length - current.Offset
			current = current.Parent
			ensureProperty()
		},
		OnArrayBegin: func(s Span) bool {
			current = attach(newNode(NodeArray, s))
			return true
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

// FindNodeAtLocation returns the node at path, or nil. A path element that
// isn't a string or int gives nil too.
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
// includeRightBound also matches the offset just past a node's end; what an
// editor wants with the caret right after a token.
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
