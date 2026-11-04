package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

func server(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL+"/", Static("tok"), srv.Client())
	require.NoError(t, err)
	return c
}

func TestNewRefusesWhatIsNotACloudURL(t *testing.T) {
	for _, bad := range []string{"", "ftp://x", "http://", "::"} {
		_, err := New(bad, Static("t"), nil)
		require.Error(t, err, bad)
	}
	c, err := New("https://build.example.com/base/", Static("t"), nil)
	require.NoError(t, err)
	require.Equal(t, "https://build.example.com/base", c.Base())
}

func TestPathSegmentsAreEscaped(t *testing.T) {
	var got string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath()
		w.Write([]byte("[]"))
	})
	_, err := c.Jobs(t.Context(), "a/b c")
	require.NoError(t, err)
	require.Equal(t, "/jobs/a%2Fb%20c", got)
}

func TestRefusalsCarryTheirStatus(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusUnauthorized: "tau login",
		http.StatusNotFound:     "not yours",
		http.StatusConflict:     "409: conflict",
		http.StatusBadGateway:   "the cloud answered 502",
	} {
		c := server(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			if status == http.StatusConflict {
				w.Write([]byte(`{"error":"conflict"}`))
			}
		})
		err := c.Cancel(t.Context(), "j")
		require.ErrorContains(t, err, want)
		require.True(t, IsStatus(err, status))
		require.False(t, IsStatus(err, 999))
	}
}

func TestNoTokenIsRefusedBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, nil, nil)
	require.NoError(t, err)
	require.ErrorContains(t, c.Retry(t.Context(), "j"), "no token")
	require.Zero(t, hits.Load())
}

func TestUnreachable(t *testing.T) {
	c, err := New("http://127.0.0.1:1", Static("t"), nil)
	require.NoError(t, err)
	_, err = c.Job(t.Context(), "j")
	require.Error(t, err)
	_, err = c.Jobs(t.Context(), "p")
	require.Error(t, err)
	_, err = c.LinkProject(t.Context(), "n", "a", nil)
	require.Error(t, err)
	_, err = c.Projects(t.Context())
	require.Error(t, err)
}

func TestLinkingAndListingProjects(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /projects/QmShop":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, []any{}, body["code_repository_keys"], "no code repositories is an empty list, not null")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"QmShop","config_repository_key":"github.com/a/c","code_repository_keys":[]}`))
		case "GET /projects":
			w.Write([]byte(`[{"id":"QmShop","config_repository_key":"github.com/a/c"}]`))
		case "GET /cloud":
			w.Write([]byte(`{"fqdn":"tau.example"}`))
		case "GET /projects/QmShop/grants":
			w.Write([]byte(`[{"id":"g1","user":"u-1","role":"viewer","by":"u-0","at":"2026-10-21T00:00:00Z"}]`))
		case "POST /projects/QmShop/grants":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"g2"}`))
		case "DELETE /projects/QmShop/grants/g1":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"project QmTaken is already claimed by github.com/x/y; generate a new id in project.yaml"}`))
		}
	})
	p, err := c.LinkProject(t.Context(), "QmShop", "github.com/a/c", nil)
	require.NoError(t, err)
	require.Equal(t, "github.com/a/c", p.Config)

	ps, err := c.Projects(t.Context())
	require.NoError(t, err)
	require.Len(t, ps, 1)

	fqdn, err := c.Fqdn(t.Context())
	require.NoError(t, err)
	require.Equal(t, "tau.example", fqdn)
	gs, err := c.Grants(t.Context(), "QmShop")
	require.NoError(t, err)
	require.Equal(t, "viewer", gs[0].Role)
	gid, err := c.GrantRole(t.Context(), "QmShop", "u-2", "builder")
	require.NoError(t, err)
	require.Equal(t, "g2", gid)
	require.NoError(t, c.Revoke(t.Context(), "QmShop", "g1"))
	_, err = c.GrantRole(t.Context(), "QmNone", "u-2", "builder")
	require.Error(t, err)

	_, err = c.LinkProject(t.Context(), "QmTaken", "github.com/a/c", []string{"github.com/a/d"})
	require.True(t, IsStatus(err, http.StatusConflict))
	require.ErrorContains(t, err, "generate a new id in project.yaml")
}

func TestAJobThatIsNotAJob(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not json")) })
	_, err := c.Job(t.Context(), "j")
	require.ErrorContains(t, err, "reading job j")
}

func TestSecretsRoundTrip(t *testing.T) {
	var body []byte
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		switch r.Method {
		case http.MethodPut:
			body, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			w.Write([]byte(`["A"]`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	require.NoError(t, c.SetSecret(t.Context(), "p", "prod", "A", []byte("v")))
	require.Equal(t, []byte("v"), body)
	names, err := c.Secrets(t.Context(), "p", "prod")
	require.NoError(t, err)
	require.Equal(t, []string{"A"}, names)
	require.NoError(t, c.UnsetSecret(t.Context(), "p", "prod", "A"))
}

// eventsOf is text as the build service serves a run's log from byte from: an event per
// line, one JSON object per line, each carrying the offset after it.
func eventsOf(text string, from int) []string {
	var out []string
	for at := from; at < len(text); {
		end := len(text)
		if i := strings.IndexByte(text[at:], '\n'); i >= 0 {
			end = at + i + 1
		}
		raw, _ := protojson.Marshal(&Event{Kind: &buildv2.Job_Event_Message{Message: strings.TrimSuffix(text[at:end], "\n")}, Offset: int64(end)})
		out = append(out, string(raw)+"\n")
		at = end
	}
	return out
}

// logServer serves log's events from ?offset=, as run 1 unless asked for another,
// cutting the connection inside the event after the first cut on the first `drops`
// requests. It keeps the query each request asked with.
func logServer(t *testing.T, log string, drops int, cut int) (*Client, *atomic.Int32, func() []string) {
	var requests atomic.Int32
	var mu sync.Mutex
	var asked []string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		mu.Lock()
		asked = append(asked, r.URL.RawQuery)
		mu.Unlock()
		run := r.URL.Query().Get("attempt")
		if run == "" {
			run = "1"
		}
		from, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		events := eventsOf(log, from)
		if int(n) <= drops {
			part := strings.Join(events[:min(cut, len(events))], "")
			if cut < len(events) {
				part += events[cut][:len(events[cut])/2]
			}
			conn, buf, _ := w.(http.Hijacker).Hijack()
			buf.WriteString("HTTP/1.1 200 OK\r\nX-Job-Attempt: " + run + "\r\nTransfer-Encoding: chunked\r\n\r\n" + strconv.FormatInt(int64(len(part)), 16) + "\r\n" + part + "\r\n")
			buf.Flush()
			conn.Close()
			return
		}
		w.Header().Set("X-Job-Attempt", run)
		io.WriteString(w, strings.Join(events, ""))
	})
	return c, &requests, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

const fiveLines = "a\nb\nc\nd\ne\n"

func TestTheStreamResumesAtTheOffsetReached(t *testing.T) {
	c, requests, _ := logServer(t, fiveLines, 2, 2)
	s := c.Stream(t.Context(), "j", 0, 0)
	s.Backoff = time.Millisecond
	var out bytes.Buffer
	end, err := s.CopyTo(&out)
	require.NoError(t, err)
	require.Equal(t, fiveLines, out.String(), "an event cut in the middle is read again whole")
	require.Equal(t, int64(len(fiveLines)), end)
	require.Equal(t, int64(len(fiveLines)), s.Offset())
	require.Equal(t, int32(3), requests.Load())
}

func TestTheStreamResumesTheRunItWasReading(t *testing.T) {
	c, _, asked := logServer(t, fiveLines, 2, 2)
	s := c.Stream(t.Context(), "j", 0, 0)
	s.Backoff = time.Millisecond
	_, err := s.CopyTo(io.Discard)
	require.NoError(t, err)
	require.Equal(t, int32(1), s.Attempt())
	require.Equal(t, []string{"", "attempt=1&offset=4", "attempt=1&offset=8"}, asked())

	c, _, asked = logServer(t, fiveLines, 1, 2)
	s = c.Stream(t.Context(), "j", 7, 2)
	s.Backoff = time.Millisecond
	var out bytes.Buffer
	_, err = s.CopyTo(&out)
	require.NoError(t, err)
	require.Equal(t, "b\nc\nd\ne\n", out.String())
	require.Equal(t, []string{"attempt=7&offset=2", "attempt=7&offset=6"}, asked())
}

func TestTheStreamRefusesAnotherRunsLog(t *testing.T) {
	requests := 0
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			first := eventsOf("first\n", 0)[0]
			conn, buf, _ := w.(http.Hijacker).Hijack()
			buf.WriteString("HTTP/1.1 200 OK\r\nX-Job-Attempt: 1\r\nTransfer-Encoding: chunked\r\n\r\n" + strconv.FormatInt(int64(len(first)), 16) + "\r\n" + first + "\r\n")
			buf.Flush()
			conn.Close()
			return
		}
		w.Header().Set("X-Job-Attempt", "2")
		io.WriteString(w, strings.Join(eventsOf("first\nof the second run\n", 6), ""))
	})
	s := c.Stream(t.Context(), "j", 0, 0)
	s.Backoff = time.Millisecond
	var out bytes.Buffer
	_, err := s.CopyTo(&out)
	require.ErrorContains(t, err, "attempt 1 was asked for and attempt 2 was sent")
	require.Equal(t, "first\n", out.String())
}

func TestALostRunEndsTheStream(t *testing.T) {
	gone := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Job-Attempt", "4")
		w.WriteHeader(http.StatusGone)
		io.WriteString(w, `{"error":"this run's log was not kept"}`)
	}
	c := server(t, gone)

	_, err := c.Stream(t.Context(), "j", 0, 0).CopyTo(io.Discard)
	var lost *LostLog
	require.ErrorAs(t, err, &lost)
	require.Equal(t, LostLog{Job: "j", Attempt: 4}, *lost)
	require.Equal(t, "job j: the log of attempt 4 was not kept", err.Error())

	_, err = c.Stream(t.Context(), "j", 2, 9).CopyTo(io.Discard)
	require.ErrorAs(t, err, &lost)
	require.Equal(t, LostLog{Job: "j", Attempt: 2, Offset: 9}, *lost)
	require.Equal(t, "job j: the log of attempt 2 was not kept past byte 9", err.Error())
}

func TestAResponseThatNamesNoRunIsRefused(t *testing.T) {
	requests := 0
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		io.WriteString(w, strings.Join(eventsOf("a log\n", 0), ""))
	})
	_, err := c.Stream(t.Context(), "j", 0, 0).CopyTo(io.Discard)
	require.ErrorIs(t, err, errUnnamedRun)
	require.Equal(t, 1, requests)
}

func TestAnEventThatIsNotOneEndsTheStream(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Job-Attempt", "1")
		io.WriteString(w, "not an event\n")
	})
	_, err := c.Stream(t.Context(), "j", 0, 0).CopyTo(io.Discard)
	require.ErrorContains(t, err, "reading the log of j")
}

func TestTheStreamGivesUpAfterItsRetries(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	s := c.Stream(t.Context(), "j", 0, 0)
	s.Backoff, s.Retries = time.Millisecond, 2
	_, err := s.CopyTo(io.Discard)
	require.Error(t, err)
}

func TestTheStreamStopsOnARefusal(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := c.Stream(t.Context(), "j", 0, 0).CopyTo(io.Discard)
	require.True(t, IsStatus(err, http.StatusNotFound))
	var refusal *Error
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "7", refusal.Header.Get("Retry-After"), "a refusal keeps the headers it came with")
}

func TestTheStreamRetriesAnUnreachableCloudThenStops(t *testing.T) {
	c, err := New("http://127.0.0.1:1", Static("t"), nil)
	require.NoError(t, err)
	s := c.Stream(t.Context(), "j", 0, 0)
	s.Backoff, s.Retries = time.Millisecond, 1
	_, err = s.Next()
	require.Error(t, err)

	// A CALLER THAT GIVES UP while the stream waits to ask again ends the wait.
	ctx, cancel := context.WithCancel(t.Context())
	hangsUp := server(t, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	s = hangsUp.Stream(ctx, "j", 0, 0)
	s.Backoff = time.Hour
	_, err = s.Next()
	require.Error(t, err)
}

func TestTheStreamStopsWhenItsWriterDoes(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Job-Attempt", "1")
		io.WriteString(w, strings.Join(eventsOf("data\n", 0), ""))
	})
	_, err := c.Stream(t.Context(), "j", 0, 0).CopyTo(failing{})
	require.Error(t, err)
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestTheStreamAsksForASnapshotOnlyWhenTold(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("X-Job-Attempt", "1")
		from, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		io.WriteString(w, strings.Join(eventsOf("abc"+"so far\n", from), ""))
	})

	s := c.Stream(t.Context(), "j", 0, 3)
	s.Snapshot = true
	var out bytes.Buffer
	end, err := s.CopyTo(&out)
	require.NoError(t, err)
	require.Equal(t, "so far\n", out.String())
	require.Equal(t, int64(3+len("so far\n")), end)

	_, err = c.Stream(t.Context(), "j", 0, 0).CopyTo(io.Discard)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"/job/j/logs?offset=3&snapshot=true", "/job/j/logs?"}, asked)
}

func TestTheStreamStopsWhenCancelledDuringBackoff(t *testing.T) {
	c, _, _ := logServer(t, fiveLines, 100, 1)
	ctx, cancel := context.WithCancel(t.Context())
	s := c.Stream(ctx, "j", 0, 0)
	s.Backoff = time.Hour
	line, err := s.Line()
	require.NoError(t, err)
	require.Equal(t, "a\n", string(line))
	cancel()
	_, err = s.Next()
	require.Error(t, err)
}

// THE STATUS A RUN ENDED THE JOB IN is the log's own last event, and the stream says
// it, so a follower knows how the run ended without asking the record.
func TestTheStreamSaysHowTheRunEnded(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Job-Attempt", "1")
		io.WriteString(w, strings.Join(eventsOf("built\n", 0), ""))
		raw, _ := protojson.Marshal(&Event{Kind: &buildv2.Job_Event_Status{Status: buildv2.JobStatus_JobStatusSuccess}, Offset: 6})
		w.Write(append(raw, '\n'))
	})
	s := c.Stream(t.Context(), "j", 0, 0)
	_, ended := s.Ended()
	require.False(t, ended)
	var out bytes.Buffer
	_, err := s.CopyTo(&out)
	require.NoError(t, err)
	require.Equal(t, "built\n", out.String(), "a status is not a line of output")
	status, ended := s.Ended()
	require.True(t, ended)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, status)
}

func TestBuildsByHandAndByUpload(t *testing.T) {
	var got []string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+r.URL.RawQuery+" "+string(body))
		switch r.URL.Path {
		case "/cloud":
			w.Write([]byte(`{"fqdn":"tau.test","uploads":true}`))
		case "/projects/p/deployments":
			w.Write([]byte(`["prod"]`))
		case "/projects/p/deployments/prod":
			w.Write([]byte(`{"object":{}}`))
		default:
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"job_id":"j1"}`))
		}
	})

	cloud, err := c.Cloud(t.Context())
	require.NoError(t, err)
	require.Equal(t, Cloud{Fqdn: "tau.test", Uploads: true}, cloud)

	job, err := c.Build(t.Context(), "p", "git.test/a/b", "refs/heads/main", "abc")
	require.NoError(t, err)
	require.Equal(t, "j1", job)

	job, err = c.Upload(t.Context(), "p", "", "refs/heads/main", bytes.NewReader([]byte("bundle")))
	require.NoError(t, err)
	require.Equal(t, "j1", job)
	_, err = c.Upload(t.Context(), "p", "git.test/a/lib", "refs/heads/main", bytes.NewReader([]byte("code")))
	require.NoError(t, err)
	envs, err := c.Deployments(t.Context(), "p")
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, envs)

	compiled, err := c.Published(t.Context(), "p", "prod")
	require.NoError(t, err)
	require.Equal(t, `{"object":{}}`, string(compiled))

	require.Equal(t, "POST /build/p  "+`{"commit_sha":"abc","ref":"refs/heads/main","repository_key":"git.test/a/b"}`, got[1])
	require.Equal(t, "POST /upload/p ref=refs%2Fheads%2Fmain bundle", got[2])
	require.Equal(t, "POST /upload/p ref=refs%2Fheads%2Fmain&repository=git.test%2Fa%2Flib code", got[3])
}

func TestBuildsTheCloudRefuses(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"refs/heads/x is not a ref this cloud builds"}`))
	})
	_, err := c.Build(t.Context(), "p", "k", "refs/heads/x", "abc")
	require.ErrorContains(t, err, "not a ref this cloud builds")
	_, err = c.Upload(t.Context(), "p", "", "refs/heads/x", bytes.NewReader(nil))
	require.True(t, IsStatus(err, http.StatusUnprocessableEntity))
	_, err = c.Published(t.Context(), "p", "prod")
	require.Error(t, err)
	_, err = c.Cloud(t.Context())
	require.Error(t, err)
	_, err = c.Deployments(t.Context(), "p")
	require.Error(t, err)
}

func TestARefusalForRateIsSentAgainWhenTheCloudSays(t *testing.T) {
	var hits atomic.Int32
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`["job-1"]`))
	})
	ids, err := c.Jobs(t.Context(), "proj")
	require.NoError(t, err)
	require.Equal(t, []string{"job-1"}, ids)
	require.Equal(t, int32(2), hits.Load())

	// ONE THAT SAYS NOTHING, or keeps refusing, is the refusal it was.
	for _, header := range []string{"", "soon"} {
		hits.Store(0)
		c := server(t, func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if header != "" {
				w.Header().Set("Retry-After", header)
			}
			w.WriteHeader(http.StatusTooManyRequests)
		})
		_, err := c.Jobs(t.Context(), "proj")
		require.True(t, IsStatus(err, http.StatusTooManyRequests))
		require.Equal(t, int32(1), hits.Load())
	}
	hits.Store(0)
	c = server(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err = c.Jobs(t.Context(), "proj")
	require.True(t, IsStatus(err, http.StatusTooManyRequests))
	require.Equal(t, int32(1+MaxRateRetries), hits.Load(), "and it is sent again a bounded number of times")
}

func TestARequestWithABodyIsNotSentAgain(t *testing.T) {
	var hits atomic.Int32
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.Upload(t.Context(), "proj", "", "refs/heads/main", bytes.NewReader([]byte("bundle")))
	require.True(t, IsStatus(err, http.StatusTooManyRequests))
	require.Equal(t, int32(1), hits.Load(), "its body is spent")
}

func TestAWaitForTheRateEndsWithTheCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		// The caller gives up while it waits out the hour it was told.
		cancel()
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.Jobs(ctx, "proj")
	require.ErrorIs(t, err, context.Canceled)
}
