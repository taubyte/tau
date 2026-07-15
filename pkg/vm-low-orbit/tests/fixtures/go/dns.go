//go:build dns

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/dns"
	"github.com/taubyte/go-sdk/event"
)

var (
	testUrl      = "google.com"
	taubyteUrl   = "taubyte.com"
	localAddr    = "127.0.0.1"
	devPort      = 4253
	expectedPref = uint16(10)
	expectedHost = "smtp.google.com."
)

//export dnstest
func dntest(e event.Event) uint32 {
	/* Testing Default Resolver Lookup Calls */
	resolver, err := dns.NewResolver()
	if err != nil {
		panic(fmt.Errorf("failed new Resolver with %v", err))
	}

	txtRecords, err := resolver.LookupTXT(testUrl)
	if err != nil {
		panic(fmt.Errorf("failed lookup txt with %v", err))
	}

	if len(txtRecords) == 0 {
		panic(fmt.Errorf("got 0 txt records for %s", testUrl))
	}

	addrRecords, err := resolver.LookupAddress(localAddr)
	if err != nil {
		panic(fmt.Errorf("failed lookup address with %v", err))
	}

	if addrRecords[0] != "localhost" {
		panic(fmt.Errorf("local addr should be pointing to localhost. Got %s", addrRecords[0]))
	}

	cname, err := resolver.LookupCNAME(taubyteUrl)
	if err != nil {
		panic(fmt.Errorf("failed lookup cname with %v", err))
	}

	if cname != "nodes.taubyte.com." {
		panic(fmt.Errorf("expected taubyte url to point to nodes.taubyte.com. instead got %s", cname))
	}

	mxRecords, err := resolver.LookupMX(testUrl)
	if err != nil {
		panic(fmt.Errorf("failed lookup mx with %v", err))
	}

	if mxRecords == nil {
		panic(fmt.Errorf("mxRecords is nil"))
	}

	for _, mx := range mxRecords {
		if mx.Host != expectedHost && mx.Pref != expectedPref {
			panic(fmt.Errorf("did not get expected values %s != %s or %d != %d", mx.Host, expectedHost, mx.Pref, expectedPref))
		}
	}
	/* Testing Default Resolver Lookup Calls */

	/* Testing Custom Resolver Lookup Calls */
	err = resolver.Reroute("8.8.8.8:53", "udp")
	if err != nil {
		panic(fmt.Errorf("failed rerouting with %v", err))
	}

	txtRecords, err = resolver.LookupTXT(testUrl)
	if err != nil {
		panic(fmt.Errorf("failed lookup txt with %v", err))
	}

	if len(txtRecords) == 0 {
		panic(fmt.Errorf("got 0 txt records for %s", testUrl))
	}

	addrRecords, err = resolver.LookupAddress(localAddr)
	if err != nil {
		panic(fmt.Errorf("failed lookup address with %v", err))
	}

	if addrRecords[0] != "localhost" {
		panic(fmt.Errorf("local addr should be pointing to localhost. Got %s", addrRecords[0]))
	}

	cname, err = resolver.LookupCNAME(taubyteUrl)
	if err != nil {
		panic(fmt.Errorf("failed lookup cname with %v", err))
	}

	if cname != "nodes.taubyte.com." {
		panic(fmt.Errorf("expected taubyte url to point to nodes.taubyte.com. instead got %s", cname))
	}

	mxRecords, err = resolver.LookupMX(testUrl)
	if err != nil {
		panic(fmt.Errorf("failed lookup mx with %v", err))
	}

	if mxRecords == nil {
		panic(fmt.Errorf("mxRecords is nil"))
	}

	for _, mx := range mxRecords {
		if mx.Host != expectedHost && mx.Pref != expectedPref {
			panic(fmt.Errorf("did not get expected values %s != %s or %d != %d", mx.Host, expectedHost, mx.Pref, expectedPref))
		}
	}
	/* Testing Custom Resolver Lookup Calls */

	err = resolver.Reset()
	if err != nil {
		panic(fmt.Errorf("failed resetting resolver with %v", err))
	}

	h, err := e.HTTP()
	if err != nil {
		panic(err)
	}

	_, err = h.Write([]byte("DnsTest"))
	if err != nil {
		panic(err)
	}

	return 0
}
