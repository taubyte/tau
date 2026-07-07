// Package randname produces friendly "adjective_noun" names. It replaces
// moby's github.com/moby/moby/pkg/namesgenerator, which moved under an
// internal/ path (unimportable) when the Docker SDK split into submodules at
// v29. ponytail: fixed word lists (~1k combos) is plenty for naming mock
// hosts and course hyphae; callers that need uniqueness dedupe themselves.
package randname

import (
	"crypto/rand"
	"fmt"
)

var adjectives = []string{
	"admiring", "amazing", "blissful", "bold", "brave", "clever", "cool",
	"dazzling", "eager", "elegant", "fervent", "gifted", "happy", "jolly",
	"keen", "loving", "modest", "nifty", "quirky", "serene", "sharp", "sleepy",
	"stoic", "tender", "trusting", "upbeat", "vibrant", "wizardly", "youthful", "zen",
}

var nouns = []string{
	"archimedes", "bohr", "curie", "darwin", "edison", "euler", "fermi",
	"galileo", "hopper", "hawking", "kepler", "lovelace", "mendel", "newton",
	"noether", "pascal", "planck", "ptolemy", "ramanujan", "sagan", "shannon",
	"tesla", "turing", "volta", "wozniak", "yalow", "einstein", "faraday",
	"feynman", "goodall",
}

// Get returns a name like "clever_turing".
func Get() string {
	b := make([]byte, 2)
	rand.Read(b)
	return fmt.Sprintf("%s_%s", adjectives[int(b[0])%len(adjectives)], nouns[int(b[1])%len(nouns)])
}
