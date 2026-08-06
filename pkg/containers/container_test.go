package containers

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

// fakeBackend stands in for a container runtime so the run semantics — exit
// codes, log delivery, cleanup ordering — can be tested without a daemon.
type fakeBackend struct {
	mu sync.Mutex

	exitCode int
	stdout   string
	stderr   string

	startErr   error
	waitErr    error
	inspectErr error
	logsErr    error

	// logStream, when set, replaces the framed stdout/stderr streams.
	logStream io.ReadCloser

	calls   []string
	removed bool
}

func (f *fakeBackend) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeBackend) BackendType() core.BackendType     { return core.BackendTypeDocker }
func (f *fakeBackend) Image(string) core.Image           { return nil }
func (f *fakeBackend) HealthCheck(context.Context) error { return nil }
func (f *fakeBackend) Capabilities() core.BackendCapabilities {
	return core.BackendCapabilities{SupportsBuild: true}
}

func (f *fakeBackend) Create(context.Context, *core.ContainerConfig) (core.ContainerID, error) {
	f.record("create")
	return "fake-id", nil
}

func (f *fakeBackend) Start(context.Context, core.ContainerID) error {
	f.record("start")
	return f.startErr
}

func (f *fakeBackend) Stop(context.Context, core.ContainerID) error       { return nil }
func (f *fakeBackend) Clean(context.Context, time.Duration, string) error { return nil }

func (f *fakeBackend) Remove(context.Context, core.ContainerID) error {
	f.record("remove")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = true
	return nil
}

func (f *fakeBackend) Wait(context.Context, core.ContainerID) error {
	f.record("wait")
	return f.waitErr
}

func (f *fakeBackend) Inspect(context.Context, core.ContainerID) (*core.ContainerInfo, error) {
	f.record("inspect")
	if f.inspectErr != nil {
		return nil, f.inspectErr
	}
	return &core.ContainerInfo{ID: "fake-id", ExitCode: f.exitCode, Status: "exited"}, nil
}

func (f *fakeBackend) Logs(context.Context, core.ContainerID) (io.ReadCloser, error) {
	f.record("logs")
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	if f.logStream != nil {
		return f.logStream, nil
	}

	var buf bytes.Buffer
	writeFrame(&buf, 1, f.stdout)
	writeFrame(&buf, 2, f.stderr)
	return io.NopCloser(&buf), nil
}

// writeFrame writes a docker-multiplexed stream frame, the shape every backend
// returns from Logs.
func writeFrame(w io.Writer, stream byte, payload string) {
	if payload == "" {
		return
	}
	var header [8]byte
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	w.Write(header[:])
	io.WriteString(w, payload)
}

func newContainer(backend core.Backend) *Container {
	return &Container{backend: backend, id: "fake-id"}
}

func TestRunSuccess(t *testing.T) {
	backend := &fakeBackend{stdout: "build ok\n"}

	logs, err := newContainer(backend).Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, logs)

	out, err := io.ReadAll(logs.Combined())
	require.NoError(t, err)
	assert.Equal(t, "build ok\n", string(out))
	assert.True(t, backend.removed, "a finished container must be cleaned up")
}

func TestRunNonZeroExit(t *testing.T) {
	// A failed build step must report the failure AND hand back the output,
	// which is where the compiler error is.
	backend := &fakeBackend{exitCode: 2, stdout: "syntax error\n"}

	logs, err := newContainer(backend).Run(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrorExitCode, "the exit code must be reported through ErrorExitCode")
	assert.Contains(t, err.Error(), "2", "the error must carry the actual code")

	require.NotNil(t, logs, "output must still be readable when the container failed")
	out, err := io.ReadAll(logs.Combined())
	require.NoError(t, err)
	assert.Contains(t, string(out), "syntax error")
}

func TestRunReadsLogsBeforeRemoving(t *testing.T) {
	// Removing a container cuts a stream still being read from it, so the logs
	// have to be drained first. This backend fails the read if that order slips.
	backend := &fakeBackend{}
	backend.logStream = &failAfterRemoveReader{backend: backend, payload: framed("streamed output\n")}

	logs, err := newContainer(backend).Run(context.Background())
	require.NoError(t, err)

	out, err := io.ReadAll(logs.Combined())
	require.NoError(t, err, "logs must be complete before the container is removed")
	assert.Equal(t, "streamed output\n", string(out))

	assert.Equal(t, []string{"start", "wait", "inspect", "logs", "remove"}, backend.calls)
}

func TestRunStartFailure(t *testing.T) {
	backend := &fakeBackend{startErr: errors.New("no such image")}

	_, err := newContainer(backend).Run(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrorContainerStart)
	assert.NotContains(t, backend.calls, "wait", "nothing may be waited on after a failed start")
}

func TestRunWaitFailure(t *testing.T) {
	backend := &fakeBackend{waitErr: errors.New("daemon gone")}

	_, err := newContainer(backend).Run(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrorContainerWait, "a broken runtime must not be reported as a container failure")
	assert.NotErrorIs(t, err, ErrorExitCode)
}

func TestRunInspectFailure(t *testing.T) {
	backend := &fakeBackend{inspectErr: errors.New("gone")}

	_, err := newContainer(backend).Run(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrorContainerInspect)
}

func TestRunLogsFailure(t *testing.T) {
	backend := &fakeBackend{logsErr: errors.New("stream closed")}

	_, err := newContainer(backend).Run(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrorContainerLogs)
}

func TestContainerOptions(t *testing.T) {
	c := &Container{}

	for _, opt := range []ContainerOption{
		Command([]string{"echo", "hi"}),
		WorkDir("/work"),
		Volume("/host", "/container"),
		Variable("KEY", "value"),
		Variables(map[string]string{"OTHER": "thing"}),
	} {
		require.NoError(t, opt(c))
	}

	config := convertToContainerConfig("alpine", c)

	assert.Equal(t, "alpine", config.Image)
	assert.Equal(t, []string{"echo", "hi"}, config.Command)
	assert.Equal(t, "/work", config.WorkDir)
	assert.ElementsMatch(t, []string{"KEY=value", "OTHER=thing"}, config.Env)
	require.Len(t, config.Volumes, 1)
	assert.Equal(t, core.VolumeMount{Source: "/host", Destination: "/container"}, config.Volumes[0])
}

func framed(payload string) string {
	var buf bytes.Buffer
	writeFrame(&buf, 1, payload)
	return buf.String()
}

// failAfterRemoveReader serves its payload but starts failing once the
// container has been removed, standing in for a runtime that cuts the stream.
type failAfterRemoveReader struct {
	backend *fakeBackend
	payload string
	reader  *strings.Reader
}

func (r *failAfterRemoveReader) Read(p []byte) (int, error) {
	r.backend.mu.Lock()
	removed := r.backend.removed
	r.backend.mu.Unlock()
	if removed {
		return 0, errors.New("stream cut: container was removed while its logs were being read")
	}

	if r.reader == nil {
		r.reader = strings.NewReader(r.payload)
	}
	return r.reader.Read(p)
}

func (r *failAfterRemoveReader) Close() error { return nil }
