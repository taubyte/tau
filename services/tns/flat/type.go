package flat

import (
	"sort"
)

type Object struct {
	Root []string
	data interface{}
	Data Items
}

type Item struct {
	Path []string
	Data interface{}
}

type Items []Item

// Sort sorts Items by Path in lexicographic order
func (items Items) Sort() {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		// Compare paths element by element
		minLen := len(a.Path)
		if len(b.Path) < minLen {
			minLen = len(b.Path)
		}
		for idx := 0; idx < minLen; idx++ {
			if a.Path[idx] < b.Path[idx] {
				return true
			}
			if a.Path[idx] > b.Path[idx] {
				return false
			}
		}
		// If one path is a prefix of the other, shorter comes first
		return len(a.Path) < len(b.Path)
	})
}
