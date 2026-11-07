package joblogs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	httpClient "github.com/taubyte/tau/clients/http"
	patrickHttp "github.com/taubyte/tau/clients/http/patrick"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

// fakePatrick serves /job/<id>, /logs/<cid> and /stream/logs/<id> like Patrick.
func fakePatrick(t *testing.T) Client {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/j1":
			json.NewEncoder(w).Encode(map[string]any{"Job": patrick.Job{Id: "j1", Status: patrick.JobStatusFailed, Logs: map[string]string{"res1": "cid1"}}})
		case "/job/j2":
			json.NewEncoder(w).Encode(map[string]any{"Job": patrick.Job{Id: "j2", Status: patrick.JobStatusSuccess, Logs: map[string]string{"2026-11-07T16:27:54Z": "cid1", "j2": "cid1"}}})
		case "/logs/cid1":
			io.WriteString(w, "step 1\nerror: go build failed\n")
		case "/stream/logs/j1":
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			for _, l := range []string{"line 1\n", "line 2\n"} {
				c.WriteMessage(websocket.TextMessage, []byte(l))
			}
			c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := patrickHttp.New(context.Background(), httpClient.URL(srv.URL), httpClient.Auth("t"))
	assert.NilError(t, err)
	return c
}

func TestShowJobLogs(t *testing.T) {
	var buf bytes.Buffer
	assert.NilError(t, Show(&buf, fakePatrick(t), "j1", false))
	assert.Assert(t, strings.Contains(buf.String(), "resource res1"), buf.String())
	assert.Assert(t, strings.Contains(buf.String(), "error: go build failed"), buf.String())
	assert.Assert(t, strings.Contains(buf.String(), "(failed)"), buf.String())
}

func TestShowFollowStreams(t *testing.T) {
	var buf bytes.Buffer
	assert.NilError(t, Show(&buf, fakePatrick(t), "j1", true))
	assert.Equal(t, buf.String(), "line 1\nline 2\n")
}

func TestShowUnknownJob(t *testing.T) {
	assert.Assert(t, Show(io.Discard, fakePatrick(t), "nope", false) != nil)
}

func TestFunctionLogsSaySo(t *testing.T) {
	app := &cli.App{Commands: []*cli.Command{Command}}
	err := app.Run([]string{"tau", "logs", "function", "fn1"})
	assert.ErrorIs(t, err, ErrFunctionLogs)
	assert.Assert(t, !strings.Contains(ErrFunctionLogs.Error(), "\n"))
}

// B5: one log filed under a timestamp and the job id was printed twice.
func TestShowLogOnceUnderJobID(t *testing.T) {
	var buf bytes.Buffer
	assert.NilError(t, Show(&buf, fakePatrick(t), "j2", false))
	assert.Equal(t, strings.Count(buf.String(), "error: go build failed"), 1, buf.String())
	assert.Assert(t, strings.Contains(buf.String(), "resource j2"), buf.String())
}
