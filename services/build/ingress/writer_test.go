package ingress

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// A caller hanging up mid-listing is ordinary, so write failures are a path.

// breakingWriter is an http.ResponseWriter whose Write fails from the nth call
// on — a client that went away.
type breakingWriter struct {
	*httptest.ResponseRecorder
	after int
	calls int
}

func (b *breakingWriter) Write(p []byte) (int, error) {
	b.calls++
	if b.calls > b.after {
		return 0, errors.New("connection reset by peer")
	}
	return b.ResponseRecorder.Write(p)
}

func TestAListStopsWhenTheCallerGoesAway(t *testing.T) {
	for _, after := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("after %d writes", after), func(t *testing.T) {
			h := newHarness(t, Config{})
			emitted := 0
			h.backend.list = func(_ string, emit func(string) error) error {
				for _, id := range []string{"a", "b", "c", "d"} {
					if err := emit(id); err != nil {
						return err
					}
					emitted++
				}
				return nil
			}

			w := &breakingWriter{ResponseRecorder: httptest.NewRecorder(), after: after}
			h.Handler().ServeHTTP(w, authed(http.MethodGet, "/jobs/"+testProject, testToken))

			require.Less(t, emitted, 4, "the listing stopped rather than running to the end")
			require.Contains(t, h.logs.joined(), "connection reset")
		})
	}
}

func TestAJobThatCannotBeEncodedIsNotHalfWritten(t *testing.T) {
	h := newHarness(t, Config{})
	h.backend.get = func(id string) (*buildv2.Job, error) {
		return &buildv2.Job{Id: id, RepositoryKey: testRepoKey, Ref: "refs/heads/\xff"}, nil
	}

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.NotContains(t, bodyOf(t, w), "refs/heads")
	require.Contains(t, h.logs.joined(), "encoding job")
}
