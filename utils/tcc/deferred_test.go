package tccUtils

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// TestWHATACOMPILECOULDNOTCHECKISSAIDOUTLOUD.
//
// A region name is an opaque label the implementation defines — the config
// holds no list of regions and never will — so it is refusable only where the
// cloud is finally in the room. A compile that reported nothing about that
// would read as "your config is checked", and the first time an author learned
// their label meant nothing would be a deploy that placed their database
// somewhere they never wrote.
func TestDeferredNamesWhatIsOutstandingByValidator(t *testing.T) {
	out := Deferred([]engine.NextValidation{
		{Key: "region", Validator: "region", Value: "eu"},
		{Key: "region", Validator: "region", Value: "us"},
		{Key: "zone", Validator: "dns", Value: "shop.example.com"},
	})

	require.Equal(t, []string{"eu", "us"}, out["region"])
	require.Equal(t, []string{"shop.example.com"}, out["dns"])
}

// TestONELABELONCE, however many resources named it: an author reading this
// wants the set they have to be right about, not a count of their resources.
func TestDeferredReportsEachValueOnce(t *testing.T) {
	out := Deferred([]engine.NextValidation{
		{Validator: "region", Value: "eu"},
		{Validator: "region", Value: "eu"},
		{Validator: "region", Value: "eu"},
	})
	require.Equal(t, []string{"eu"}, out["region"])
}

// TestTHESAMEVALUEUNDERTWOVALIDATORSISTWOQUESTIONS, because who answers them
// differs: a cloud says whether a region exists, a resolver says whether a name
// is proved.
func TestDeferredKeepsValidatorsApart(t *testing.T) {
	out := Deferred([]engine.NextValidation{
		{Validator: "region", Value: "eu"},
		{Validator: "dns", Value: "eu"},
	})
	require.Equal(t, []string{"eu"}, out["region"])
	require.Equal(t, []string{"eu"}, out["dns"])
}

// TestANONSTRINGISNOTALABEL and is skipped rather than rendered as one — a
// project id validation carries a string, but nothing here promises that every
// validator will.
func TestDeferredSkipsWhatIsNotAString(t *testing.T) {
	out := Deferred([]engine.NextValidation{
		{Validator: "region", Value: 7},
		{Validator: "region", Value: ""},
		{Validator: "region", Value: "eu"},
	})
	require.Equal(t, []string{"eu"}, out["region"])
}
