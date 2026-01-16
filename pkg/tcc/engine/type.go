package engine

func (t Type) String() string {
	return []string{"Int", "Bool", "Float", "String", "StringSlice"}[t]
}
