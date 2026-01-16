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
