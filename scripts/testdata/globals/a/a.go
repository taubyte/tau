// Package a is what the guard must catch, beside what it must leave alone.
package a

import (
	"errors"
	"regexp"
	s "sync"
	at "sync/atomic"
)

var (
	ErrX     = errors.New("x")
	re       = regexp.MustCompile("x")
	pool     = s.Pool{}
	derived  = s.OnceValue(func() int { return 1 })
	table    = map[string]int{"a": 1}
	sentinel = &XError{}
	byInit   = map[string]int{}

	lock  s.Mutex
	gate  s.Once
	wg    s.WaitGroup
	kept  s.Map
	hits  at.Int64
	state = struct {
		s.RWMutex
		n int
	}{}
	made   = newThing()
	shared = &thing{}

	inc     int
	ranged  string
	deleted = map[string]int{}
	cleared = map[string]int{}
	copied  = make([]int, 3)
	addr    int
	swapped = 1
	Counter int
)

type XError struct{}

func (XError) Error() string { return "x" }

type thing struct{}

func newThing() *thing { return &thing{} }

func init() { byInit["a"] = 1 }

func point(*int) {}

func use() {
	inc++
	for _, ranged = range []string{"x"} {
	}
	delete(deleted, "a")
	clear(cleared)
	copy(copied, []int{1})
	point(&addr)
	shadow := table["a"]
	shadow++
	point(&shadow)
}
