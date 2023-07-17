package benchmark

import (
	"fmt"
	"testing"

	"github.com/miekg/dns"
)

/* Valid Seer Ip's
<--- GCP --->
35.226.220.13
35.230.124.202

<--- NETACTUATE--->
45.159.98.203 <- This one kind of slow
103.84.155.170
43.245.48.25
192.73.252.54
192.73.240.147
104.225.103.61
192.73.243.173
45.159.97.245


*/

var maxGoRoutines = 12
var fqdn = "nodes.taubyte.com."
var blockedFqdn = "poop.com."
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
