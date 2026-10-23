package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

// Path segments are escaped, so an id cannot add a segment of its own.
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

// ── the resumable log ────────────────────────────────────────────────────────

// logServer serves log from ?offset=, cutting the connection after cut bytes
// on the first `drops` requests.
func logServer(t *testing.T, log string, drops int, cut int) (*Client, *atomic.Int32) {
	var requests atomic.Int32
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		from, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		rest := log[from:]
		if int(n) <= drops {
			hj := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			part := rest[:min(cut, len(rest))]
			buf.WriteString("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" + strconv.FormatInt(int64(len(part)), 16) + "\r\n" + part + "\r\n")
			buf.Flush()
			conn.Close()
			return
		}
		io.WriteString(w, rest)
	})
	return c, &requests
}

func TestTheStreamResumesAtTheOffsetReached(t *testing.T) {
	c, requests := logServer(t, "0123456789", 2, 3)
	s := c.Stream(t.Context(), "j", 0)
	s.Backoff = time.Millisecond
	var out bytes.Buffer
	end, err := s.CopyTo(&out)
	require.NoError(t, err)
	require.Equal(t, "0123456789", out.String())
	require.Equal(t, int64(10), end)
	require.Equal(t, int64(10), s.Offset())
	require.Equal(t, int32(3), requests.Load())
}

func TestTheStreamGivesUpAfterItsRetries(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	s := c.Stream(t.Context(), "j", 0)
	s.Backoff, s.Retries = time.Millisecond, 2
	_, err := s.CopyTo(io.Discard)
	require.Error(t, err)
}

func TestTheStreamStopsOnARefusal(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	_, err := c.Stream(t.Context(), "j", 0).CopyTo(io.Discard)
	require.True(t, IsStatus(err, http.StatusNotFound))
}

func TestTheStreamRetriesAnUnreachableCloudThenStops(t *testing.T) {
	c, err := New("http://127.0.0.1:1", Static("t"), nil)
	require.NoError(t, err)
	s := c.Stream(t.Context(), "j", 0)
	s.Backoff, s.Retries = time.Millisecond, 1
	_, err = s.Next()
	require.Error(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	s = c.Stream(ctx, "j", 0)
	s.Backoff = time.Hour
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err = s.Next()
	require.Error(t, err)
}

func TestTheStreamStopsWhenItsWriterDoes(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "data") })
	_, err := c.Stream(t.Context(), "j", 0).CopyTo(failing{})
	require.Error(t, err)
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// A cancelled context during a backoff after a mid-stream drop ends the read.
func TestTheStreamStopsWhenCancelledDuringBackoff(t *testing.T) {
	c, _ := logServer(t, "0123456789", 100, 2)
	ctx, cancel := context.WithCancel(t.Context())
	s := c.Stream(ctx, "j", 0)
	s.Backoff = time.Hour
	chunk, err := s.Next()
	require.NoError(t, err)
	require.Equal(t, "01", string(chunk))
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err = s.Next()
	require.Error(t, err)
}

func TestBuildsByHandAndByUpload(t *testing.T) {
	var got []string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+r.URL.RawQuery+" "+string(body))
		switch r.URL.Path {
		case "/cloud":
			w.Write([]byte(`{"fqdn":"tau.test","uploads":true}`))
		case "/projects/p/deployments/prod":
			w.Write([]byte(`{"object":{}}`))
		case "/job/j1/logs/k":
			w.Write([]byte("the log"))
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

	job, err = c.Upload(t.Context(), "p", "refs/heads/main", bytes.NewReader([]byte("bundle")))
	require.NoError(t, err)
	require.Equal(t, "j1", job)

	compiled, err := c.Published(t.Context(), "p", "prod")
	require.NoError(t, err)
	require.Equal(t, `{"object":{}}`, string(compiled))

	log, err := c.StoredLog(t.Context(), "j1", "k")
	require.NoError(t, err)
	body, err := io.ReadAll(log)
	log.Close()
	require.NoError(t, err)
	require.Equal(t, "the log", string(body))

	require.Equal(t, "POST /build/p  "+`{"commit_sha":"abc","ref":"refs/heads/main","repository_key":"git.test/a/b"}`, got[1])
	require.Equal(t, "POST /upload/p ref=refs%2Fheads%2Fmain bundle", got[2])
}

func TestBuildsTheCloudRefuses(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"refs/heads/x is not a ref this cloud builds"}`))
	})
	_, err := c.Build(t.Context(), "p", "k", "refs/heads/x", "abc")
	require.ErrorContains(t, err, "not a ref this cloud builds")
	_, err = c.Upload(t.Context(), "p", "refs/heads/x", bytes.NewReader(nil))
	require.True(t, IsStatus(err, http.StatusUnprocessableEntity))
	_, err = c.Published(t.Context(), "p", "prod")
	require.Error(t, err)
	_, err = c.Cloud(t.Context())
	require.Error(t, err)
	_, err = c.StoredLog(t.Context(), "j", "k")
	require.Error(t, err)
}
