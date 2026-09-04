package wasm

import (
	"testing"

	"github.com/samyfodil/wazy/component"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// WHAT COMES BACK WAS PRODUCED BY A STRANGER'S COMPONENT. The happy path is
// covered against a real guest; these are the shapes a guest that does not
// implement this world produces, and every one of them has to be an error
// naming the export rather than a panic three frames later.

func TestLiftingAResponseThatIsNotOne(t *testing.T) {
	for name, value := range map[string]component.Value{
		"not a record":    "hello",
		"too few fields":  []component.Value{uint32(200)},
		"too many fields": []component.Value{uint32(200), nil, []byte{}, "extra"},
		"a status that is not one": []component.Value{
			"200", []component.Value{}, []byte{},
		},
		"a body that is not bytes": []component.Value{
			uint32(200), []component.Value{}, "a string",
		},
		"headers that are not a list": []component.Value{
			uint32(200), "not a list", []byte{},
		},
		"a header that is not a pair": []component.Value{
			uint32(200), []component.Value{[]component.Value{"only"}}, []byte{},
		},
		"a header that is not two strings": []component.Value{
			uint32(200), []component.Value{[]component.Value{"name", uint32(1)}}, []byte{},
		},
		"a header that is not a record": []component.Value{
			uint32(200), []component.Value{"flat"}, []byte{},
		},
	} {
		_, err := responseFrom(value, "checkout")
		require.ErrorIs(t, err, ErrNotAHandler, name)
		require.ErrorContains(t, err, "checkout", name)
	}
}

// A guest that answers with no headers and no body answers with a status, and
// that is a complete response rather than a malformed one.
func TestLiftingAnEmptyResponse(t *testing.T) {
	res, err := responseFrom([]component.Value{uint32(204), nil, nil}, "checkout")
	require.NoError(t, err)
	require.Equal(t, 204, res.Status)
	require.Nil(t, res.Headers)
	require.Nil(t, res.Body)

	res, err = responseFrom([]component.Value{uint32(200), []component.Value{}, []byte{}}, "checkout")
	require.NoError(t, err)
	require.Nil(t, res.Headers, "an empty list is no headers, not an empty map somebody has to check")
	require.Empty(t, res.Body)
}

// A RECORD IS ITS FIELDS IN ORDER, which makes the field order in
// wit/handler.wit load-bearing: reordering it there without reordering it here
// is a silent transposition of two strings.
func TestLoweringARequest(t *testing.T) {
	fields, ok := requestValue(&invoke.Request{
		Method:  "POST",
		Path:    "/orders",
		Headers: map[string][]string{"b": {"2"}, "a": {"1", "1b"}},
		Body:    []byte("payload"),
	}).([]component.Value)
	require.True(t, ok)
	require.Len(t, fields, 4)

	require.Equal(t, "POST", fields[0])
	require.Equal(t, "/orders", fields[1])
	require.Equal(t, []byte("payload"), fields[3])

	// SORTED, AND REPEATS KEPT IN ORDER, so two instances of one revision are
	// handed the same list for the same request — a map's range order is not a
	// thing to hand a guest.
	require.Equal(t, []component.Value{
		[]component.Value{"a", "1"},
		[]component.Value{"a", "1b"},
		[]component.Value{"b", "2"},
	}, fields[2])

	// A NIL BODY IS AN EMPTY LIST AND NOT A NULL: the world says list<u8>,
	// which has no absent case.
	fields, _ = requestValue(&invoke.Request{}).([]component.Value)
	require.Equal(t, []byte{}, fields[3])
	require.Empty(t, fields[2])
}

// ROUNDED UP, because a ceiling below what the author asked for is a workload
// that fails for a reason the configuration does not state — and the overshoot
// is at most one page.
func TestTheMemoryCeilingInPages(t *testing.T) {
	require.Equal(t, uint32(0), memoryPages(0), "no ceiling declared is no ceiling set")
	require.Equal(t, uint32(0), memoryPages(-1))

	require.Equal(t, uint32(1), memoryPages(1))
	require.Equal(t, uint32(1), memoryPages(pageSize))
	require.Equal(t, uint32(2), memoryPages(pageSize+1))
	require.Equal(t, uint32(489), memoryPages(32_000_000), "32MB, the fixture's own function limit")

	// THE 32-BIT ADDRESS SPACE IS THE REAL CEILING. A larger number panics
	// inside wazy rather than being honoured, so it is clamped here.
	require.Equal(t, uint32(1<<16), memoryPages(1<<40))
}

// The environment is sorted, so two instances of one revision are handed the
// same list.
func TestTheEnvironmentIsSorted(t *testing.T) {
	require.Nil(t, environment(nil))
	require.Nil(t, environment(map[string]string{}))
	require.Equal(t,
		[]string{"A=1", "B=2", "C=3"},
		environment(map[string]string{"C": "3", "A": "1", "B": "2"}))
}
