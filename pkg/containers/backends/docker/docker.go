package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/taubyte/tau/pkg/containers/core"
)

// DockerBackend implements the core.Backend interface for Docker
type DockerBackend struct {
	config     core.DockerConfig
	client     *client.Client
	containers map[core.ContainerID]string // Map container ID to Docker container ID
}

// New creates a new Docker backend
func New(config core.DockerConfig) (*DockerBackend, error) {
	backend := &DockerBackend{
		config:     config,
		containers: make(map[core.ContainerID]string),
	}

	if err := backend.initClient(); err != nil {
		return nil, fmt.Errorf("failed to initialize Docker client: %w", err)
	}

	if err := backend.HealthCheck(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to connect to Docker daemon: %w", err)
	}

	return backend, nil
}

// resolveDockerHost returns the Docker host to use, matching the docker CLI when possible.
// When config.Host and DOCKER_HOST are unset, reads the current context from the Docker
// config file (same location and layout as the Docker CLI: ~/.docker/config.json or
// DOCKER_CONFIG) and the context endpoint from the context store (contexts/<name>/meta.json
// or contexts/meta/<hash>/meta.json per Docker's store layout). This keeps behaviour
// portable and aligned with the Docker CLI without adding a direct dependency on
// github.com/docker/cli (which can pull in incompatible moby/client versions).
// Returns ("", false) when no host should be forced.
func resolveDockerHost(configHost string) (host string, use bool) {
	if configHost != "" {
		return configHost, true
	}
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h, true
	}
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		dir = filepath.Join(home, ".docker")
	}
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", false
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.CurrentContext == "" {
		return "", false
	}
	if cfg.CurrentContext == "default" {
		return "", false
	}
	// Try legacy path: contexts/<name>/meta.json
	metaPath := filepath.Join(dir, "contexts", cfg.CurrentContext, "meta.json")
	data, err = os.ReadFile(metaPath)
	if err != nil {
		// Docker stores context metadata under contexts/meta/<hash>/meta.json with Name field
		metaDir := filepath.Join(dir, "contexts", "meta")
		entries, listErr := os.ReadDir(metaDir)
		if listErr != nil {
			return "", false
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(metaDir, e.Name(), "meta.json")
			data, err = os.ReadFile(path)
			if err != nil {
				continue
			}
			var meta struct {
				Name      string `json:"Name"`
				Endpoints struct {
					Docker struct {
						Host string `json:"Host"`
					} `json:"docker"`
				} `json:"Endpoints"`
			}
			if json.Unmarshal(data, &meta) != nil || meta.Name != cfg.CurrentContext || meta.Endpoints.Docker.Host == "" {
				continue
			}
			return meta.Endpoints.Docker.Host, true
		}
		return "", false
	}
	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &meta); err != nil || meta.Endpoints.Docker.Host == "" {
		return "", false
	}
	return meta.Endpoints.Docker.Host, true
}

// initClient initializes the Docker client
func (b *DockerBackend) initClient() error {
	var opts []client.Opt

	host, useHost := resolveDockerHost(b.config.Host)
	if useHost {
		opts = append(opts, client.WithHost(host))
	}

	if b.config.APIVersion != "" {
		opts = append(opts, client.WithAPIVersion(b.config.APIVersion))
	}

	cli, err := client.New(opts...)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	b.client = cli
	return nil
}

// BackendType returns core.BackendTypeDocker
func (b *DockerBackend) BackendType() core.BackendType {
	return core.BackendTypeDocker
}

// Image returns an Image interface for the given image name
func (b *DockerBackend) Image(name string) core.Image {
	return &dockerImage{
		backend: b,
		name:    name,
	}
}

// Create creates a new container
func (b *DockerBackend) Create(ctx context.Context, config *core.ContainerConfig) (core.ContainerID, error) {
	if b.client == nil {
		return "", fmt.Errorf("Docker client not initialized")
	}

	containerID := core.ContainerID(fmt.Sprintf("tau-%s-%d", time.Now().Format("20060102-150405"), time.Now().Nanosecond()))

	containerConfig, hostConfig, networkingConfig, err := b.createDockerConfig(config)
	if err != nil {
		return "", fmt.Errorf("failed to create Docker config: %w", err)
	}

	resp, err := b.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           containerConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
		Name:             string(containerID),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	b.containers[containerID] = resp.ID

	return containerID, nil
}

// createDockerConfig converts core.ContainerConfig to Docker API types
func (b *DockerBackend) createDockerConfig(config *core.ContainerConfig) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	containerConfig := &container.Config{
		Image:      config.Image,
		Cmd:        config.Command,
		Env:        config.Env,
		WorkingDir: config.WorkDir,
	}

	if len(containerConfig.Env) == 0 {
		containerConfig.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	}

	if containerConfig.WorkingDir == "" {
		containerConfig.WorkingDir = "/"
	}

	hostConfig := &container.HostConfig{}
	if config.Resources != nil {
		hostConfig.Resources = container.Resources{}

		if config.Resources.Memory > 0 {
			hostConfig.Resources.Memory = config.Resources.Memory
		}

		if config.Resources.MemorySwap > 0 {
			hostConfig.Resources.MemorySwap = config.Resources.MemorySwap
		} else if config.Resources.MemorySwap == -1 {
			hostConfig.Resources.MemorySwap = -1
		}

		if config.Resources.CPUQuota > 0 {
			hostConfig.Resources.CPUQuota = config.Resources.CPUQuota
		}

		if config.Resources.CPUPeriod > 0 {
			hostConfig.Resources.CPUPeriod = config.Resources.CPUPeriod
		}

		if config.Resources.CPUShares > 0 {
			hostConfig.Resources.CPUShares = config.Resources.CPUShares
		}

		if config.Resources.PIDs > 0 {
			hostConfig.Resources.PidsLimit = &config.Resources.PIDs
		}
	}

	if len(config.Volumes) > 0 {
		hostConfig.Mounts = make([]mount.Mount, 0, len(config.Volumes))
		for _, vol := range config.Volumes {
			mountType := mount.TypeBind
			if vol.IsVolumeName {
				mountType = mount.TypeVolume
			}

			hostConfig.Mounts = append(hostConfig.Mounts, mount.Mount{
				Type:     mountType,
				Source:   vol.Source,
				Target:   vol.Destination,
				ReadOnly: vol.ReadOnly,
			})
		}
	}

	networkingConfig := &network.NetworkingConfig{}
	if config.Network != nil {
		if config.Network.Mode != "" {
			hostConfig.NetworkMode = container.NetworkMode(config.Network.Mode)
		}

		if len(config.Network.PortMappings) > 0 {
			hostConfig.PortBindings = make(network.PortMap)
			for _, pm := range config.Network.PortMappings {
				protocol := pm.Protocol
				if protocol == "" {
					protocol = "tcp"
				}
				portKey, err := network.ParsePort(fmt.Sprintf("%d/%s", pm.ContainerPort, protocol))
				if err != nil {
					return nil, nil, nil, fmt.Errorf("invalid port %d/%s: %w", pm.ContainerPort, protocol, err)
				}

				binding := network.PortBinding{
					HostPort: fmt.Sprintf("%d", pm.HostPort),
				}
				if pm.HostIP != "" {
					hostIP, err := netip.ParseAddr(pm.HostIP)
					if err != nil {
						return nil, nil, nil, fmt.Errorf("invalid host IP %q: %w", pm.HostIP, err)
					}
					binding.HostIP = hostIP
				}

				hostConfig.PortBindings[portKey] = append(hostConfig.PortBindings[portKey], binding)
			}
		}

		for _, d := range config.Network.DNS {
			addr, err := netip.ParseAddr(d)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("invalid DNS address %q: %w", d, err)
			}
			hostConfig.DNS = append(hostConfig.DNS, addr)
		}
	}

	return containerConfig, hostConfig, networkingConfig, nil
}

// getDockerID gets the Docker container ID for the given container ID
// Tries the map first, then falls back to looking up by name
func (b *DockerBackend) getDockerID(ctx context.Context, id core.ContainerID) (string, error) {
	if dockerID, ok := b.containers[id]; ok {
		return dockerID, nil
	}

	res, err := b.client.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("name", string(id)),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	if len(res.Items) > 0 {
		b.containers[id] = res.Items[0].ID
		return res.Items[0].ID, nil
	}

	return "", fmt.Errorf("container %s not found", id)
}

// Start starts a container
func (b *DockerBackend) Start(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	dockerID, err := b.getDockerID(ctx, id)
	if err != nil {
		return err
	}

	if _, err := b.client.ContainerStart(ctx, dockerID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container %s: %w", id, err)
	}

	return nil
}

// Stop stops a container
func (b *DockerBackend) Stop(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	dockerID, err := b.getDockerID(ctx, id)
	if err != nil {
		return err
	}

	timeoutSeconds := 10
	if _, err := b.client.ContainerStop(ctx, dockerID, client.ContainerStopOptions{Timeout: &timeoutSeconds}); err != nil {
		return fmt.Errorf("failed to stop container %s: %w", id, err)
	}

	return nil
}

// Remove removes a container
func (b *DockerBackend) Remove(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	dockerID, err := b.getDockerID(ctx, id)
	if err != nil {
		return err
	}

	if _, err := b.client.ContainerRemove(ctx, dockerID, client.ContainerRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("failed to remove container %s: %w", id, err)
	}

	delete(b.containers, id)

	return nil
}

// Clean removes images older than age that match the given filter.
func (b *DockerBackend) Clean(ctx context.Context, age time.Duration, filter client.Filters) error {
	if b.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	opts := client.ImageListOptions{}
	if len(filter) > 0 {
		opts.Filters = filter
	}
	res, err := b.client.ImageList(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}

	cutoff := time.Now().Add(-age).Unix()
	for _, img := range res.Items {
		if img.Created < cutoff {
			// best effort to remove the image
			b.client.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{
				Force:         true,
				PruneChildren: true,
			})
		}
	}
	return nil
}

// Wait waits for a container to exit
func (b *DockerBackend) Wait(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	dockerID, err := b.getDockerID(ctx, id)
	if err != nil {
		return err
	}

	wait := b.client.ContainerWait(ctx, dockerID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})

	select {
	case err := <-wait.Error:
		if err != nil {
			return fmt.Errorf("failed to wait for container %s: %w", id, err)
		}
	case status := <-wait.Result:
		if status.StatusCode != 0 {
			return fmt.Errorf("container %s exited with status %d", id, status.StatusCode)
		}
	}

	return nil
}

// Logs returns logs for a container
func (b *DockerBackend) Logs(ctx context.Context, id core.ContainerID) (io.ReadCloser, error) {
	if b.client == nil {
		return nil, fmt.Errorf("Docker client not initialized")
	}

	dockerID, err := b.getDockerID(ctx, id)
	if err != nil {
		return nil, err
	}

	logs, err := b.client.ContainerLogs(ctx, dockerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get logs for container %s: %w", id, err)
	}

	return logs, nil
}

// Inspect returns information about a container
func (b *DockerBackend) Inspect(ctx context.Context, id core.ContainerID) (*core.ContainerInfo, error) {
	if b.client == nil {
		return nil, fmt.Errorf("Docker client not initialized")
	}

	dockerID, err := b.getDockerID(ctx, id)
	if err != nil {
		return nil, err
	}

	inspect, err := b.client.ContainerInspect(ctx, dockerID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s: %w", id, err)
	}
	info := inspect.Container

	containerInfo := &core.ContainerInfo{
		ID:     id,
		Image:  info.Config.Image,
		Status: string(info.State.Status),
	}

	if info.State.ExitCode != 0 {
		containerInfo.ExitCode = info.State.ExitCode
	}

	if info.State.StartedAt != "" {
		startedAt, err := time.Parse(time.RFC3339Nano, info.State.StartedAt)
		if err == nil {
			containerInfo.StartedAt = startedAt
		}
	}

	if info.HostConfig != nil && info.HostConfig.Resources.Memory > 0 {
		containerInfo.Resources = &core.ResourceLimits{
			Memory:     info.HostConfig.Resources.Memory,
			MemorySwap: info.HostConfig.Resources.MemorySwap,
			CPUQuota:   info.HostConfig.Resources.CPUQuota,
			CPUPeriod:  info.HostConfig.Resources.CPUPeriod,
			CPUShares:  info.HostConfig.Resources.CPUShares,
			PIDs:       0,
		}
		if info.HostConfig.Resources.PidsLimit != nil {
			containerInfo.Resources.PIDs = *info.HostConfig.Resources.PidsLimit
		}
	}

	return containerInfo, nil
}

// HealthCheck performs a health check on the backend
func (b *DockerBackend) HealthCheck(ctx context.Context) error {
	if b.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	_, err := b.client.Ping(ctx, client.PingOptions{})
	if err != nil {
		return fmt.Errorf("Docker daemon not responding: %w", err)
	}

	return nil
}

// Capabilities returns the backend capabilities
func (b *DockerBackend) Capabilities() core.BackendCapabilities {
	return core.BackendCapabilities{
		SupportsMemory:     true,
		SupportsCPU:        true,
		SupportsStorage:    true,
		SupportsPIDs:       true,
		SupportsMemorySwap: true,
		SupportsBuild:      true,
		SupportsOCI:        true,
		SupportsNetworking: true,
		SupportsVolumes:    true,
	}
}
