package object

type resolver[T DataTypes] struct {
	root Object[T]
}

func NewResolver[T DataTypes](root Object[T]) Resolver[T] {
	return &resolver[T]{root: root}
}

func (r *resolver[T]) Root() Object[T] {
	return r.root
}

func (r *resolver[T]) Resolve(path ...string) (Object[T], error) {
	var err error
	cur := r.root
	for _, p := range path {
		cur, err = cur.Child(p).Object()
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}
