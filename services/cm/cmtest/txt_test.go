package cmtest

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestAZoneAnswersWhatWasSetThroughAResolver(t *testing.T) {
	z := Serve(t)
	long := strings.Repeat("claim", 100)
	z.Set("_tau.Shop.Test", long)

	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, z.Addr)
	}}
	got, err := r.LookupTXT(t.Context(), "_tau.shop.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != long {
		t.Fatalf("the record came back as %d records, the first %d bytes", len(got), len(got[0]))
	}
	if _, err := r.LookupTXT(t.Context(), "nothing.shop.test"); err == nil {
		t.Fatal("a name nothing was set at answered")
	}
}
