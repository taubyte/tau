package path

import (
	"reflect"
	"testing"
	"time"
)

func TestSplit(t *testing.T) {
	cases := map[string][]string{
		"/home/taubyte":  {"home", "taubyte"},
		"home/taubyte":   {"home", "taubyte"},
		"file/doc.bin/1": {"file", "doc.bin", "1"},
		"/a//b/":         {"a", "b"},
		"/a/../b":        {"b"},
		"/":              {},
		"":               {},
		".":              {},
		"name":           {"name"},
	}
	for in, want := range cases {
		done := make(chan []string, 1)
		go func() { done <- Split(in) }()
		select {
		case got := <-done:
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Split(%q) = %q, want %q", in, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("Split(%q) never returned", in)
		}
	}
}
