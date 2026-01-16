package object

import (
	"errors"
	"regexp"
	"strings"
)

type selector[T DataTypes] struct {
	parent *object[T]
	obj    *object[T]
	name   string
	err    error
}

type object[T DataTypes] struct {
	children   map[string]*object[T]
	data       map[string]T
	regexCache map[string]*regexp.Regexp // Cache compiled regex patterns per object instance
}

func New[T DataTypes]() Object[T] {
	return &object[T]{
		children:   make(map[string]*object[T]),
		data:       make(map[string]T),
		regexCache: make(map[string]*regexp.Regexp),
	}
}

func (o *object[T]) Map() map[string]any {
	// Pre-allocate map with capacity for attributes + children
	m := make(map[string]any, 1+len(o.children))
	m["attributes"] = o.data
	for n, o := range o.children {
		m[n] = o.Map()
	}
	return m
}

func (o *object[T]) Flat() map[string]any {
	// Pre-allocate map with capacity for data + children
	m := make(map[string]any, len(o.data)+len(o.children))
	for n, o := range o.data {
		m[n] = o
	}
	for n, o := range o.children {
		m[n] = o.Flat()
	}
	return m
}

func (o *object[T]) Children() []string {
	ret := make([]string, 0, len(o.children))
	for k := range o.children {
		ret = append(ret, k)
	}
	return ret
}

func (o *object[T]) Child(sel any) Selector[T] {
	switch _sel := sel.(type) {
	case string:
		return o.newNameSelector(_sel)
	case *object[T]: // save interface conv
		return o.newPtrSelector(_sel)
	case Object[T]:
		obj, ok := _sel.(*object[T])
		if !ok {
			return o.newErrorSelector(errors.New("unknown object type"))
		}
		return o.newPtrSelector(obj)
	default:
		return o.newErrorSelector(errors.New("unknown object type"))
	}
}

func (o *object[T]) Fetch(path ...string) (Object[T], error) {
	if len(path) == 0 {
		return o, nil
	}

	co, err := o.Child(path[0]).Object()
	if err != nil {
		return nil, err
	}

	for _, p := range path[1:] {
		co, err = co.Child(p).Object()
		if err != nil {
			return nil, err
		}
	}

	return co, nil
}

func (o *object[T]) CreatePath(path ...string) (Object[T], error) {
	if len(path) == 0 {
		return o, nil
	}

	co, err := o.Child(path[0]).Object()
	if err == ErrNotExist {
		if err := o.Child(path[0]).Add(New[T]()); err != nil {
			return nil, err
		}
		co, _ = o.Child(path[0]).Object()
	} else if err != nil {
		return nil, err
	}

	for _, p := range path[1:] {
		_co, err := co.Child(p).Object()
		if err == ErrNotExist {
			if err := co.Child(p).Add(New[T]()); err != nil {
				return nil, err
			}
			_co, _ = co.Child(p).Object()
		} else if err != nil {
			return nil, err
		}
		co = _co
	}

	return co, nil
}

func (o *object[T]) Set(name string, data T) {
	o.data[name] = data
}

func (o *object[T]) Get(name string) T {
	return o.data[name]
}

func (o *object[T]) GetString(name string) (string, error) {
	val, exists := o.data[name]
	if !exists {
		return "", ErrNotExist
	}

	// Handle Refrence type (any)
	if str, ok := any(val).(string); ok {
		return str, nil
	}

	return "", errors.New("value is not a string")
}

func (o *object[T]) GetInt(name string) (int, error) {
	val, exists := o.data[name]
	if !exists {
		return 0, ErrNotExist
	}

	// Handle Refrence type (any)
	switch v := any(val).(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case uint:
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		return int(v), nil
	case uint64:
		return int(v), nil
	case float32:
		// Only accept floats that represent whole numbers
		if float32(int(v)) != v {
			return 0, errors.New("value is not an integer")
		}
		return int(v), nil
	case float64:
		// Only accept floats that represent whole numbers
		if float64(int(v)) != v {
			return 0, errors.New("value is not an integer")
		}
		return int(v), nil
	}

	return 0, errors.New("value is not an integer")
}

func (o *object[T]) GetBool(name string) (bool, error) {
	val, exists := o.data[name]
	if !exists {
		return false, ErrNotExist
	}

	// Handle Refrence type (any)
	if b, ok := any(val).(bool); ok {
		return b, nil
	}

	return false, errors.New("value is not a boolean")
}

func (o *object[T]) Delete(name string) {
	delete(o.data, name)
}

func (o *object[T]) Move(from, to string) error {
	if _, ok := o.data[from]; !ok {
		return ErrNotExist
	}

	o.data[to] = o.data[from]
	delete(o.data, from)
	return nil
}

func (o *object[T]) getObjectName(obj *object[T]) string {
	for name, _obj := range o.children {
		if obj == _obj {
			return name
		}
	}
	return ""
}

func (o *object[T]) getObjectByName(name string) (*object[T], error) {
	if obj, ok := o.children[name]; ok {
		return obj, nil
	}
	return nil, ErrNotExist
}

func (o *object[T]) exists(name string, obj *object[T]) bool {
	if _, ok := o.children[name]; ok {
		return true
	}

	for _, _obj := range o.children {
		if obj == _obj {
			return true
		}
	}

	return false
}

func (o *object[T]) set(name string, obj *object[T]) error {
	if obj == nil {
		return errors.New("nil object")
	}
	o.children[name] = obj
	return nil
}

func exactMatch(a, b string) bool {
	return a == b
}

func (o *object[T]) Match(expr string, mtype MatchType) ([]Object[T], error) {
	var cmp func(string, string) bool
	switch mtype {
	case ExactMatch:
		cmp = exactMatch
	case PrefixMatch:
		cmp = strings.HasPrefix
	case SuffixMatch:
		cmp = strings.HasSuffix
	case SubMatch:
		cmp = strings.Contains
	case RegExMatch:
		// Check per-object cache first (WASI-compatible, no race conditions)
		cexpr, cached := o.regexCache[expr]
		if !cached {
			var err error
			cexpr, err = regexp.Compile(expr)
			if err != nil {
				return nil, err
			}
			// Cache the compiled regex at object level
			if o.regexCache == nil {
				o.regexCache = make(map[string]*regexp.Regexp)
			}
			o.regexCache[expr] = cexpr
		}
		cmp = func(str, _ string) bool {
			return cexpr.MatchString(str)
		}
	default:
		return nil, errors.New("unknown match type")
	}

	// Pre-allocate matches slice with estimated capacity
	matches := make([]Object[T], 0, len(o.children))
	for name, _obj := range o.children {
		if cmp(name, expr) {
			matches = append(matches, _obj)
		}
	}

	return matches, nil
}

func (o *object[T]) newNameSelector(name string) *selector[T] {
	return &selector[T]{parent: o, name: name}
}

func (o *object[T]) newPtrSelector(obj *object[T]) *selector[T] {
	return &selector[T]{parent: o, obj: obj}
}

func (o *object[T]) newErrorSelector(err error) *selector[T] {
	return &selector[T]{parent: o, err: err}
}

func (s *selector[T]) Name() string {
	if s.err != nil {
		return ""
	}
	if len(s.name) == 0 {
		s.name = s.parent.getObjectName(s.obj)
	}
	return s.name
}

func (s *selector[T]) Rename(name string) error {
	if s.err != nil {
		return s.err
	}
	if name == s.name {
		return nil
	}

	if _, exists := s.parent.children[name]; exists {
		return errors.New("name already exists")
	}

	if _, exists := s.parent.children[s.name]; !exists {
		return errors.New("child does not exist")
	}

	s.parent.children[name] = s.parent.children[s.name]
	delete(s.parent.children, s.name)
	s.name = name
	return nil
}

func (s *selector[T]) Exists() bool {
	if s.err != nil {
		return false
	}
	return s.parent.exists(s.name, s.obj)
}

func (s *selector[T]) Object() (Object[T], error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.obj == nil {
		var err error
		s.obj, err = s.parent.getObjectByName(s.name)
		if err != nil {
			return nil, err
		}
	}
	return s.obj, nil
}

func (s *selector[T]) Set(name string, data T) error {
	if s.err != nil {
		return s.err
	}
	if s.obj == nil {
		s.obj = New[T]().(*object[T])
	}
	s.obj.Set(name, data)
	return s.parent.set(s.name, s.obj)
}

func (s *selector[T]) Add(o Object[T]) error {
	if s.err != nil {
		return s.err
	}
	var ok bool
	s.obj, ok = o.(*object[T])
	if !ok {
		return errors.New("unkown object type")
	}
	return s.parent.set(s.name, s.obj)
}

func (s *selector[T]) Get(name string) (ret T, err error) {
	if s.err != nil {
		return ret, s.err
	}
	if s.obj == nil {
		if _, err = s.Object(); err != nil {
			return
		}
	}

	ret = s.obj.Get(name)

	return
}

func (s *selector[T]) GetString(name string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if s.obj == nil {
		if _, err := s.Object(); err != nil {
			return "", err
		}
	}
	return s.obj.GetString(name)
}

func (s *selector[T]) GetInt(name string) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.obj == nil {
		if _, err := s.Object(); err != nil {
			return 0, err
		}
	}
	return s.obj.GetInt(name)
}

func (s *selector[T]) GetBool(name string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if s.obj == nil {
		if _, err := s.Object(); err != nil {
			return false, err
		}
	}
	return s.obj.GetBool(name)
}

func (s *selector[T]) Delete(name string) {
	if s.err != nil {
		return
	}
	if s.obj == nil {
		if _, err := s.Object(); err != nil {
			return
		}
	}
	delete(s.obj.data, name)
}

func (s *selector[T]) Move(from, to string) error {
	if s.err != nil {
		return s.err
	}
	if s.obj == nil {
		if _, err := s.Object(); err != nil {
			return ErrNotExist
		}
	}

	if _, ok := s.obj.data[from]; !ok {
		return ErrNotExist
	}

	s.obj.data[to] = s.obj.data[from]
	delete(s.obj.data, from)

	return nil
}
