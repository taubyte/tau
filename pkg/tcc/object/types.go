package object

import (
	"errors"
)

type MatchType int

const (
	ExactMatch MatchType = iota
	PrefixMatch
	SuffixMatch
	SubMatch
	RegExMatch
)

var (
	ErrNotExist = errors.New("does not exist")
)

type DataTypes interface {
	Opaque | Refrence
}

type Object[T DataTypes] interface {
	Children() []string
	Child(any) Selector[T]
	CreatePath(path ...string) (Object[T], error)
	Fetch(path ...string) (Object[T], error)
	Set(string, T)
	Get(string) T
	GetString(string) (string, error)
	GetInt(string) (int, error)
	GetBool(string) (bool, error)
	// DeleteValue drops the VALUE at a name and leaves any subtree under it. The two
	// live in separate maps, so a name can hold both — a resolved reference nests
	// `{kind: id}` under the same name the authored string sat at, and dropping the
	// name whole would take an access block written there with it.
	DeleteValue(string)
	Delete(string)
	Move(string, string) error // rename attribute
	Map() map[string]any
	Flat() map[string]any
	Match(string, MatchType) ([]Object[T], error)
}

type Selector[T DataTypes] interface {
	Name() string
	Exists() bool
	Rename(string) error       // rename self in parent
	Move(string, string) error // rename attribute
	Set(string, T) error
	Get(string) (T, error)
	GetString(string) (string, error)
	GetInt(string) (int, error)
	GetBool(string) (bool, error)
	Delete(string)
	Add(Object[T]) error
	Object() (Object[T], error)
}

type Opaque []byte

type Refrence any

type Resolver[T DataTypes] interface {
	Root() Object[T]
	Resolve(path ...string) (Object[T], error)
}
