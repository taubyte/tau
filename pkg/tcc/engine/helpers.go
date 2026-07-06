package engine

func stringify(a any) string {
	switch b := a.(type) {
	case string:
		return b
	case StringMatcher:
		return b.String()
	case AttributeValidator:
		return "AttributeValidator()"
	case []StringMatch:
		ret := ""
		for i, s := range b {
			ret += stringify(s)
			if i < len(b)-1 {
				ret += "/"
			}
		}
		return ret
	default:
		return "unknown"
	}
}

func defAttr(name string, _type Type, options []Option) *Attribute {
	a := &Attribute{
		Name: name,
		Type: _type,
	}

	for _, opt := range options {
		opt(a)
	}

	return a
}

func Int(name string, options ...Option) *Attribute {
	return defAttr(name, TypeInt, options)
}

func Bool(name string, options ...Option) *Attribute {
	return defAttr(name, TypeBool, options)
}

func Float(name string, options ...Option) *Attribute {
	return defAttr(name, TypeFloat, options)
}

func String(name string, options ...Option) *Attribute {
	return defAttr(name, TypeString, options)
}

func StringSlice(name string, options ...Option) *Attribute {
	return defAttr(name, TypeStringSlice, options)
}

func Attributes(attrs ...*Attribute) []*Attribute {
	return attrs
}

func Root(attrs []*Attribute, children ...*Node) *Node {
	return &Node{
		Group:      true,
		Attributes: attrs,
		Children:   children,
	}
}

func Define(match StringMatch, attrs []*Attribute, children ...*Node) *Node {
	return &Node{
		Group:      false,
		Match:      match,
		Attributes: attrs,
		Children:   children,
	}
}

func DefineGroup(match string, children ...*Node) *Node {
	return &Node{
		Group:    true,
		Match:    match,
		Children: children,
	}
}

func DefineIter(attrs []*Attribute, extra ...any) *Node {
	n := &Node{
		Group:      false,
		Match:      StringMatchAll{},
		Attributes: attrs,
	}
	for _, e := range extra {
		switch v := e.(type) {
		case *Node:
			n.Children = append(n.Children, v)
		case NodeOption:
			v(n)
		}
	}
	return n
}

func DefineIterGroup(attrs []*Attribute, children ...*Node) *Node {
	return &Node{
		Group:      true,
		Match:      StringMatchAll{},
		Attributes: attrs,
		Children:   children,
	}
}
