package interp

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestResourceInRefusesWhatIsNotThere(t *testing.T) {
	obj := object.New[object.Refrence]()
	scope := object.New[object.Refrence]()
	zones := object.New[object.Refrence]()
	zone := object.New[object.Refrence]()
	zone.Set("fqdn", "example.com")
	assert.NilError(t, zones.Child("QmZone").Add(zone))
	assert.NilError(t, scope.Child("zone").Add(zones))
	assert.NilError(t, obj.Child("shop").Add(scope))

	held, err := resourceIn(obj, "shop", "zone", "QmZone")
	assert.NilError(t, err)
	assert.Equal(t, held.Flat()["fqdn"], "example.com")

	_, err = resourceIn(obj, "nowhere", "zone", "QmZone")
	assert.ErrorContains(t, err, `scope "nowhere"`)
	_, err = resourceIn(obj, "shop", "function", "QmZone")
	assert.ErrorContains(t, err, `kind "function"`)

	// refused rather than created: a missing id must not become a new resource
	_, err = resourceIn(obj, "shop", "zone", "QmOther")
	assert.ErrorContains(t, err, "no zone with id QmOther")
	assert.Assert(t, !zones.Child("QmOther").Exists())
}

func TestInlinedInOfNothingIsNothing(t *testing.T) {
	assert.Assert(t, inlinedIn(nil) == nil)
}
