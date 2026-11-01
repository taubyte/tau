package regex

import (
	"fmt"
	"regexp"
	"sync"
	"testing"
)

func reset() {
	mu.Lock()
	cache = map[string]*regexp.Regexp{}
	mu.Unlock()
}

func size() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(cache)
}

func TestAPatternIsCompiledOnce(t *testing.T) {
	reset()
	first, err := Compile(`^room/[a-z0-9]+$`)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(`^room/[a-z0-9]+$`)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("the same pattern compiled twice")
	}
}

func TestItAgreesWithTheStdlibOneShotForm(t *testing.T) {
	reset()
	cases := []struct{ pattern, subject string }{
		{`^/api/v[0-9]+/.*$`, "/api/v2/users"},
		{`^/api/v[0-9]+/.*$`, "/static/app.js"},
		{`^room/[a-z0-9]+$`, "room/lobby42"},
		{`^tenant/[^/]+/events/.*$`, "tenant/acme/events/billing"},
		{`.*`, ""},
	}
	for _, c := range cases {
		want, wantErr := regexp.MatchString(c.pattern, c.subject)
		got, gotErr := MatchString(c.pattern, c.subject)
		if got != want || (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("%q against %q: stdlib (%v,%v), cached (%v,%v)",
				c.pattern, c.subject, want, wantErr, got, gotErr)
		}
	}
}

func TestAnUncompilablePatternIsNotCached(t *testing.T) {
	reset()
	if _, err := Compile(`([a-z`); err == nil {
		t.Fatal("a pattern that is not a pattern compiled")
	}
	if got, err := MatchString(`([a-z`, "anything"); err == nil || got {
		t.Fatalf("MatchString on a bad pattern: (%v, %v)", got, err)
	}
	if size() != 0 {
		t.Fatalf("a bad pattern took a slot: cache holds %d", size())
	}
}

func TestOverTheCapItStillAnswers(t *testing.T) {
	reset()
	for i := range max + 50 {
		if _, err := Compile(fmt.Sprintf(`^fill-%d-[a-z]+$`, i)); err != nil {
			t.Fatal(err)
		}
	}
	if size() > max {
		t.Fatalf("cache grew past the cap: %d > %d", size(), max)
	}

	// The one that could not be remembered is still answered, correctly.
	got, err := MatchString(fmt.Sprintf(`^fill-%d-[a-z]+$`, max+49), fmt.Sprintf("fill-%d-abc", max+49))
	if err != nil || !got {
		t.Fatalf("an uncached pattern stopped working: (%v, %v)", got, err)
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	reset()
	const goroutines = 32
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 100 {
				pattern := fmt.Sprintf(`^ch-%d/[a-z]+$`, i%8)
				ok, err := MatchString(pattern, fmt.Sprintf("ch-%d/abc", i%8))
				if err != nil || !ok {
					t.Errorf("goroutine %d: (%v, %v)", g, ok, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

func BenchmarkOneShotStdlib(b *testing.B) {
	for b.Loop() {
		regexp.MatchString(`^room/[a-z0-9]+$`, "room/lobby42")
	}
}

func BenchmarkCached(b *testing.B) {
	reset()
	for b.Loop() {
		MatchString(`^room/[a-z0-9]+$`, "room/lobby42")
	}
}
