package config

type ListParser[T comparable] interface {
	List() []T
	Add(T) error
	Append(...T) error
	Set(...T) error
	Delete(T) error
	Clear() error
}

type list[T comparable] leaf

func (a *list[T]) List() (l []T) {
	a.Fork().Value(&l)
	return
}

func (a *list[T]) Add(v T) error {
	return a.Fork().Set(appendNew(a.List(), v)).Commit()
}

func (a *list[T]) Append(v ...T) error {
	return a.Fork().Set(appendNew(a.List(), v...)).Commit()
}

func (a *list[T]) Set(v ...T) error {
	return a.Fork().Set(v).Commit()
}

func (a *list[T]) Clear() error {
	return a.Fork().Set([]T{}).Commit()
}

func (a *list[T]) Delete(v T) error {
	e := a.List()
	l := make([]T, 0, len(e))
	for _, i := range e {
		if i != v {
			l = append(l, i)
		}
	}
	return a.Fork().Set(l).Commit()
}
