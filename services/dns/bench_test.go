package dns

import (
	"fmt"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

func BenchmarkAnswer(b *testing.B) {
	nodes := make([]stats.Node, 8)
	for i := range nodes {
		nodes[i] = stats.Node{Peer: fmt.Sprintf("p%d", i), Load: float64(i) / 10, Ready: true,
			Addresses: []string{fmt.Sprintf("203.0.113.%d", i+1), fmt.Sprintf("2001:db8::%d", i+1)}}
	}
	s, err := New(Config{}, WithRouter(serving("shop.example.com", nodes...)))
	require.NoError(b, err)
	type testCase struct {
		name  string
		qtype uint16
	}
	for _, c := range []testCase{{"A", dns.TypeA}, {"AAAA", dns.TypeAAAA}, {"miss", dns.TypeA}} {
		name := "shop.example.com"
		if c.name == "miss" {
			name = "nobody.example.com"
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s.Answer(query(name, c.qtype))
			}
		})
	}
}
