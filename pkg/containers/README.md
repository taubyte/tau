# containers

[![License](https://img.shields.io/github/license/taubyte/tau)](../../LICENSE)
[![GoDoc](https://godoc.org/github.com/taubyte/tau/pkg/containers?status.svg)](https://pkg.go.dev/github.com/taubyte/tau/pkg/containers)
[![Discord](https://img.shields.io/discord/973677117722202152?color=%235865f2&label=discord)](https://tau.link/discord)

Runs containers from Go, over docker or containerd.

## Layout

| Package | What it is |
| --- | --- |
| `containers` | The client: images, containers, run, logs. What callers use. |
| `core` | The `Backend` and `Image` interfaces, the config types, the backend registry. |
| `backends/docker` | Docker backend. Can build images. |
| `backends/containerd` | containerd backend, Linux only. Cannot build images. |
| `backends/conformance` | The behaviour both backends are held to. |
| `gc` | Periodic image cleanup. |

`New()` picks a backend at construction: docker if its daemon answers, otherwise
containerd. Callers do not choose.

## Usage

```go
client, err := containers.New()
if err != nil {
    return err
}

image, err := client.Image(ctx, "alpine:latest")
if err != nil {
    return err
}

container, err := image.Instantiate(ctx,
    containers.Command([]string{"echo", "Hello World!"}),
    containers.Volume("/host/path", "/container/path"),
    containers.Variable("KEY", "value"),
)
if err != nil {
    return err
}

logs, err := container.Run(ctx)
// logs are readable even when err wraps ErrorExitCode: that is where a failed
// build's compiler output lives.
if logs != nil {
    io.Copy(os.Stdout, logs.Combined())
}
```

`Run` starts the container, waits for it, collects its output and removes it. A
non-zero exit comes back as an error wrapping `ErrorExitCode`; a runtime that
broke comes back as one of the other sentinels in `errors.go`. Both match with
`errors.Is`.

### Building an image from a Dockerfile

The Dockerfile and anything it needs go in a tarball, which becomes an
`ImageOption`:

```bash
tar cvf image.tar -C <dir>/ .
```

```go
client.Image(ctx, "org/name:version", containers.Build(tarball))
```

Image names must be lowercase. Building needs a backend that supports it —
check `Capabilities().SupportsBuild`; containerd does not, having no BuildKit
wired up.

## Backend differences

Docker and containerd are interchangeable for running containers: same exit
codes, same log framing, same environment, working directory and bind mounts,
all enforced by the conformance suite. They still differ where the runtimes do:

- **Building.** Docker only.
- **Networking.** Docker bridges by default. containerd runs without CNI, so it
  shares the host's network instead; `none` isolates, and a bridged mode is
  refused rather than silently downgraded.
- **Port mappings and named volumes.** Docker only. containerd refuses both.
- **Resource limits.** Not available on *rootless* containerd, which cannot
  create the cgroup that would enforce them; asking for them there is an error
  rather than a limit that quietly does nothing.
- **The image's `USER`.** Not applied by containerd — resolving a user name
  needs a mount of the image's snapshot that rootless is not permitted to make,
  so containers run as root. Its `ENV`, `WORKDIR` and `ENTRYPOINT` are applied.

## Tests

```bash
go test ./pkg/containers/...   # unit tests: no daemon, no network
make test-docker               # needs a docker daemon
make test-containerd           # needs containerd, or containerd+rootlesskit
```

Both integration suites run `backends/conformance`, which is what keeps the two
backends honest. Anything a backend must do belongs there rather than in a
per-backend test.
