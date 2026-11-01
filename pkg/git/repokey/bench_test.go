package repokey_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/git/repokey"
)

var (
	sinkKey repokey.Key
	sinkErr error
)

func BenchmarkParse(b *testing.B) {
	for _, u := range []string{"https://github.com/Acme/Pay.git", "git@github.com:acme/pay"} {
		b.Run(u, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkKey, sinkErr = repokey.Parse(u)
			}
		})
	}
}
