package docker

import (
	"archive/tar"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteADD(t *testing.T) {
	for _, test := range []struct {
		name       string
		dockerfile string
		found      bool
	}{
		{"local ADD", "FROM alpine\nADD ./src /app\n", false},
		{"COPY is never remote", "FROM alpine\nCOPY ./src /app\n", false},
		{"remote ADD", "FROM alpine\nADD http://example.com/x /x\n", true},
		{"remote ADD, https", "FROM alpine\nADD https://example.com/x /x\n", true},
		{"lowercase", "FROM alpine\nadd http://example.com/x /x\n", true},
		{"after a flag", "FROM alpine\nADD --chown=1:1 http://example.com/x /x\n", true},
		{"line continuation", "FROM alpine\nADD \\\n  http://example.com/x \\\n  /x\n", true},
		{"commented out", "FROM alpine\n# ADD http://example.com/x /x\nRUN true\n", false},
		{"git source", "FROM alpine\nADD git://example.com/r.git /r\n", true},
		{"url in a RUN is not our business", "FROM alpine\nRUN wget http://example.com/x\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, found := remoteADD([]byte(test.dockerfile))
			assert.Equal(t, test.found, found, "source=%q", source)
		})
	}
}

func TestReadDockerfileKeepsTheContextUsable(t *testing.T) {
	const body = "FROM alpine\nADD http://example.com/x /x\n"

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(body))}))
	_, err := writer.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	dockerfile, buffered, err := readDockerfile(&buf, "Dockerfile")
	require.NoError(t, err)
	assert.Equal(t, body, string(dockerfile))

	// The daemon still has to receive the whole context after we have read it.
	reader := tar.NewReader(bytes.NewReader(buffered))
	header, err := reader.Next()
	require.NoError(t, err)
	assert.Equal(t, "Dockerfile", header.Name)
}

func TestReadDockerfileMissing(t *testing.T) {
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "other", Mode: 0o644, Size: 0}))
	require.NoError(t, writer.Close())

	dockerfile, buffered, err := readDockerfile(&buf, "Dockerfile")
	require.NoError(t, err, "a context without one is the daemon's error to give, not ours")
	assert.Nil(t, dockerfile)
	assert.NotEmpty(t, buffered)
}
