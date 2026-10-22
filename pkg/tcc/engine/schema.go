package engine

func (s *schemaDef) rootNode() *Node { return s.root }

func SchemaDefinition(root *Node) Schema {
	return &schemaDef{
		root: root,
	}
}
