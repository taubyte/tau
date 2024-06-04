package website

import (
	"testing"

	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
)

var paths = []string{"/", "/entry", "/entry/go", "/entry/go/noentry", "/go/entry", "/golang"}
var requestPaths = []string{"/", "/entry", "/entry/go", "/entry/go/noentry", "/go/entry", "/go"}
var failValue = matcherSpec.NoMatch
var highValue = matcherSpec.HighMatch

func getTestLen(path string) matcherSpec.Index {
	return matcherSpec.Index(len(path) + 1) // Add 1 for added slash at end
}

func TestMatch(t *testing.T) {
	score := pathContains(paths[0], requestPaths[0])
	if score != highValue {
		t.Errorf("Got %v, expected %v", score, highValue)
	}

	score = pathContains(paths[1], requestPaths[1])
	if score != highValue {
		t.Errorf("Got %v, expected %v", score, highValue)
	}

	score = pathContains(paths[2], requestPaths[2])
	if score != highValue {
		t.Errorf("Got %v, expected %v", score, highValue)
	}

	score = pathContains(paths[3], requestPaths[3])
	if score != highValue {
		t.Errorf("Got %v, expected %v", score, highValue)
	}

	score = pathContains(paths[4], requestPaths[4])
	if score != highValue {
		t.Errorf("Got %v, expected %v", score, highValue)
	}

	score = pathContains(paths[4], requestPaths[0])
	if score != failValue {
		t.Errorf("Got %v, expected %v", score, failValue)
	}

	score = pathContains(paths[3], requestPaths[2])
	if score != failValue {
		t.Errorf("Got %v, expected %v", score, failValue)
	}

	expected := getTestLen(paths[2])
	score = pathContains(paths[2], requestPaths[3])
	if score != expected {
		t.Errorf("Got %v, expected %v", score, expected)
	}

	score = pathContains(paths[4], requestPaths[1])
	if score != failValue {
		t.Errorf("Got %v, expected %v", score, failValue)
	}

	expected = getTestLen(paths[1])
	score = pathContains(paths[1], requestPaths[3])
	if score != expected {
		t.Errorf("Got %v, expected %v", score, expected)
	}

	score = pathContains(paths[1], requestPaths[4])
	if score != failValue {
		t.Errorf("Got %v, expected %v", score, failValue)
	}

	score = pathContains(paths[5], requestPaths[5])
	if score != failValue {
		t.Errorf("Got %v, expected %v", score, failValue)
	}

	score = pathContains(paths[5], requestPaths[5])
	if score != failValue {
		t.Errorf("Got %v, expected %v", score, failValue)
	}
}
