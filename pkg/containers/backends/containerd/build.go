//go:build linux

package containerd

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/taubyte/tau/pkg/containers/core"
)

// containerd cannot build images itself, so BuildKit is run as an ordinary
// container on this very backend and its output imported. Nothing has to be
// installed on the host and no daemon has to be kept alive for it: the build
// is one container that exits when it is done.
//
// Pinned rather than tracking latest — a builder that changes underneath you
// changes everyone's builds.
const buildkitImage = "docker.io/moby/buildkit:v0.32.2"

// Where the build's inputs and outputs are mounted inside that container.
const (
	buildContextPath = "/tau/context"
	buildOutputPath  = "/tau/out"
	buildOutputFile  = "image.tar"
	cgroupPath       = "/sys/fs/cgroup"
)

// buildkitFlags configures the buildkitd that buildctl-daemonless.sh starts.
// Every flag here is what makes a builder work inside a container that is
// itself unprivileged; none of them is decoration.
//
//   - no-process-sandbox: BuildKit would otherwise create a user namespace of
//     its own. Running inside a rootless containerd we are already in one, and
//     nesting fails with "newuidmap: Operation not permitted".
//   - rootless worker: the runc BuildKit runs for each step must not expect
//     cgroups of its own, or every RUN fails with "no cgroup mount found in
//     mountinfo". Tau's containers are not given a cgroup to subdivide.
//   - native snapshotter: overlayfs cannot be mounted from within a user
//     namespace, so the slower snapshotter that only copies is the one that works.
const buildkitFlags = "BUILDKITD_FLAGS=" +
	"--oci-worker-no-process-sandbox " +
	"--oci-worker-rootless " +
	"--oci-worker-snapshotter=native"

// Build builds the image from a Dockerfile by running BuildKit in a container
// and importing what it produces.
func (i *containerdImage) Build(ctx context.Context, input *core.DockerfileBuild) error {
	if i.backend.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	if input == nil || input.Context == nil {
		return fmt.Errorf("build requires a context")
	}

	workDir, err := os.MkdirTemp("", "tau-build-*")
	if err != nil {
		return fmt.Errorf("failed to create build directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	contextDir := filepath.Join(workDir, "context")
	outputDir := filepath.Join(workDir, "out")

	for _, dir := range []string{contextDir, outputDir} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
		// Explicitly, because MkdirAll applies the umask: under a rootful
		// containerd the build runs as real root and writes its result back here.
		if err := os.Chmod(dir, 0o777); err != nil {
			return fmt.Errorf("failed to open up %s: %w", dir, err)
		}
	}

	if err := extractContext(input.Context, contextDir); err != nil {
		return fmt.Errorf("failed to unpack build context: %w", err)
	}

	if err := i.runBuildKit(ctx, contextDir, outputDir, input.DockerfileName()); err != nil {
		return err
	}

	return i.importBuilt(ctx, filepath.Join(outputDir, buildOutputFile))
}

// runBuildKit runs one build and returns the builder's own output on failure —
// that output is the compiler error, the missing package, the failed RUN.
func (i *containerdImage) runBuildKit(ctx context.Context, contextDir, outputDir, dockerfile string) error {
	config := &core.ContainerConfig{
		Image: buildkitImage,
		Command: []string{
			"buildctl-daemonless.sh", "build",
			"--frontend", "dockerfile.v0",
			"--local", "context=" + buildContextPath,
			"--local", "dockerfile=" + buildContextPath,
			"--opt", "filename=" + dockerfile,
			"--output", fmt.Sprintf("type=docker,name=%s,dest=%s",
				i.name, path.Join(buildOutputPath, buildOutputFile)),
		},
		Env: []string{buildkitFlags},
		Volumes: []core.VolumeMount{
			{Source: contextDir, Destination: buildContextPath, ReadOnly: true},
			{Source: outputDir, Destination: buildOutputPath},
			// BuildKit runs a runc of its own for every step, and that runc
			// refuses to start without a cgroup hierarchy it can see: "no cgroup
			// mount found in mountinfo". Rootless containers get no cgroup mount
			// of their own, so the host's is passed through.
			{Source: cgroupPath, Destination: cgroupPath},
		},
		// BuildKit assembles layers by mounting them; the default sandbox
		// forbids that. /dev/fuse is what the snapshotter needs to do it
		// without being fully privileged.
		Privileges: &core.Privileges{
			Capabilities: []string{"SYS_ADMIN"},
			Devices:      []string{"/dev/fuse"},
			Unconfined:   true,
		},
	}

	id, err := i.backend.Create(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create builder for image %s: %w", i.name, err)
	}
	defer i.backend.Remove(context.WithoutCancel(ctx), id)

	if err := i.backend.Start(ctx, id); err != nil {
		return fmt.Errorf("failed to start builder for image %s: %w", i.name, err)
	}

	if err := i.backend.Wait(ctx, id); err != nil {
		return fmt.Errorf("failed to wait for builder of image %s: %w", i.name, err)
	}

	info, err := i.backend.Inspect(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to inspect builder of image %s: %w", i.name, err)
	}

	if info.ExitCode != 0 {
		return fmt.Errorf("building %s failed with exit code %d: %s",
			i.name, info.ExitCode, i.builderOutput(ctx, id))
	}

	return nil
}

// builderOutput reads what the builder said, for an error message. A build that
// failed without explaining why is worse than a slow one, so this reports the
// reason it could not read the output rather than returning nothing.
func (i *containerdImage) builderOutput(ctx context.Context, id core.ContainerID) string {
	logs, err := i.backend.Logs(ctx, id)
	if err != nil {
		return fmt.Sprintf("(builder output unavailable: %s)", err)
	}
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return fmt.Sprintf("(builder output unreadable: %s)", err)
	}

	// BuildKit reports progress and errors on stderr.
	out := strings.TrimSpace(stderr.String() + "\n" + stdout.String())
	if out == "" {
		return "(builder produced no output)"
	}

	return out
}

// importBuilt loads the tarball BuildKit wrote and makes it usable.
func (i *containerdImage) importBuilt(ctx context.Context, tarPath string) error {
	ctx = namespaces.WithNamespace(ctx, i.backend.config.Namespace)

	tarball, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("builder produced no image for %s: %w", i.name, err)
	}
	defer tarball.Close()

	imported, err := i.backend.client.Import(ctx, tarball)
	if err != nil {
		return fmt.Errorf("failed to import built image %s: %w", i.name, err)
	}

	if len(imported) == 0 {
		return fmt.Errorf("builder produced no image for %s", i.name)
	}

	// containerd stores images under the name inside the tarball, normalised —
	// "foo:latest" becomes "docker.io/library/foo:latest". Callers look the
	// image up by the exact name they asked to build, so that name has to
	// resolve too.
	if err := i.backend.nameImage(ctx, i.name, imported[0].Target); err != nil {
		return fmt.Errorf("failed to name built image %s: %w", i.name, err)
	}

	image, err := i.backend.client.GetImage(ctx, i.name)
	if err != nil {
		return fmt.Errorf("failed to read back built image %s: %w", i.name, err)
	}

	// Unpack: an imported image has content but no snapshot, and a container
	// cannot be created from it until it does.
	if err := image.Unpack(ctx, ""); err != nil {
		return fmt.Errorf("failed to unpack built image %s: %w", i.name, err)
	}

	return nil
}

// nameImage points name at target, creating the image record or repointing an
// existing one, so that rebuilding under a name already in use is not an error.
func (b *ContainerdBackend) nameImage(ctx context.Context, name string, target ocispec.Descriptor) error {
	service := b.client.ImageService()

	record := images.Image{Name: name, Target: target}

	if _, err := service.Create(ctx, record); err == nil {
		return nil
	} else if !errdefs.IsAlreadyExists(err) {
		return err
	}

	_, err := service.Update(ctx, record, "target")

	return err
}

// extractContext unpacks a build context tar into dir.
//
// The tar comes from a user's repository, so every entry is confined to dir: an
// entry named ../../etc/cron.d/x, or a symlink pointing at /etc/shadow, would
// otherwise write outside the build or pull host files into the image.
func extractContext(r io.Reader, dir string) error {
	reader := tar.NewReader(r)

	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading build context: %w", err)
		}

		target, err := confine(dir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFile(target, reader, header.FileInfo().Mode()); err != nil {
				return err
			}

		case tar.TypeSymlink, tar.TypeLink:
			if err := checkLink(header.Name, header.Linkname); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}

		default:
			// Devices, fifos and the like have no place in a build context.
			continue
		}
	}
}

// checkLink refuses a link that would resolve outside the build context.
//
// Unlike an entry's name, an absolute link target cannot be rooted into the
// context and made safe: it is read where it points, so /etc/shadow means the
// host's. A relative one is resolved against the link's own directory, which
// is how it will be followed, and refused if it climbs out.
func checkLink(name, linkname string) error {
	outside := fmt.Errorf("build context link %q points outside it, at %q", name, linkname)

	if filepath.IsAbs(linkname) {
		return outside
	}

	resolved := filepath.Clean(filepath.Join(filepath.Dir(name), linkname))
	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(os.PathSeparator)) {
		return outside
	}

	return nil
}

// confine joins name onto dir and refuses anything that climbs out of it.
//
// An absolute name is rooted into dir rather than refused — it cannot escape,
// and tars written from an absolute path are ordinary. A name that walks up
// with ".." is refused outright instead of being clamped: cleaning it against
// the root would silently turn "../evil" into "evil" and build something other
// than what the tar described.
func confine(dir, name string) (string, error) {
	cleaned := filepath.Clean(strings.TrimPrefix(filepath.ToSlash(name), "/"))

	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("build context entry %q escapes the build directory", name)
	}

	target := filepath.Join(dir, cleaned)

	// The join itself is checked too, so a name this function has not
	// anticipated cannot get through on the strength of the check above.
	if target != dir && !strings.HasPrefix(target, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("build context entry %q escapes the build directory", name)
	}

	return target, nil
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(file, r); err != nil {
		return err
	}

	return file.Close()
}
