package docker

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// remoteADD reports whether a Dockerfile asks the daemon to fetch a URL.
//
// ADD is the one instruction the daemon performs itself, in its own network
// namespace, rather than in a container. The egress firewall matches the network
// a restricted container runs on, so it does not and cannot cover a fetch the
// daemon makes on the build's behalf — the destination would be whatever the
// Dockerfile names. COPY does the same job for files that are in the context,
// which is where a build's own files are.
func remoteADD(dockerfile []byte) (string, bool) {
	for _, line := range instructions(dockerfile) {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "ADD") {
			continue
		}
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "--") {
				continue // a flag, not a source
			}
			if strings.Contains(field, "://") {
				return field, true
			}
		}
	}
	return "", false
}

// instructions returns a Dockerfile's instruction lines, comments dropped and
// continuations joined, which is as much parsing as remoteADD needs.
func instructions(dockerfile []byte) []string {
	var (
		out     []string
		current strings.Builder
	)

	for _, raw := range strings.Split(string(dockerfile), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasSuffix(line, "\\") {
			current.WriteString(strings.TrimSuffix(line, "\\"))
			current.WriteString(" ")
			continue
		}

		current.WriteString(line)
		out = append(out, current.String())
		current.Reset()
	}

	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// readDockerfile pulls one entry out of a build context tar, returning the tar's
// bytes as well so the caller can still hand the context to the daemon.
func readDockerfile(context io.Reader, name string) (dockerfile, buffered []byte, err error) {
	buffered, err = io.ReadAll(context)
	if err != nil {
		return nil, nil, fmt.Errorf("reading build context: %w", err)
	}

	reader := tar.NewReader(bytes.NewReader(buffered))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil, buffered, nil
		}
		if err != nil {
			return nil, buffered, fmt.Errorf("reading build context: %w", err)
		}

		if strings.TrimPrefix(header.Name, "./") != name {
			continue
		}
		if dockerfile, err = io.ReadAll(reader); err != nil {
			return nil, buffered, fmt.Errorf("reading %s from the build context: %w", name, err)
		}
		return dockerfile, buffered, nil
	}
}
