package benchmark

import (
	"fmt"
	"testing"

	"github.com/miekg/dns"
)

var maxGoRoutines = 12
var fqdn = "nodes.taubyte.com."
var blockedFqdn = "blocked.com."
var targetSeer = "45.159.97.245"

// This is used to stress test certain seers
func BenchmarkDns(b *testing.B) {
	_msg := new(dns.Msg)
	_msg.SetQuestion(fqdn, dns.TypeA)
	client := new(dns.Client)

	_msg2 := new(dns.Msg)
	_msg2.SetQuestion(blockedFqdn, dns.TypeA)
	client2 := new(dns.Client)

	b.SetParallelism(maxGoRoutines)
	for i := 0; i < b.N; i++ {

		// One valid request
		res, _, err := client.Exchange(_msg, targetSeer+":53")
		if err != nil {
			b.Error(err)
			return
		}

		fmt.Println(res)

		// One invalid request
		_, _, err = client2.Exchange(_msg2, targetSeer+":53")
		if err != nil {
			b.Error(err)
			return
		}

	}
}
