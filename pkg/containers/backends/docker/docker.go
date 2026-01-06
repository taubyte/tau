package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
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

// initClient initializes the Docker client
func (b *DockerBackend) initClient() error {
	opts := []client.Opt{
		client.WithAPIVersionNegotiation(),
	}

	if b.config.Host != "" {
		opts = append(opts, client.WithHost(b.config.Host))
	} else if host := os.Getenv("DOCKER_HOST"); host != "" {
		opts = append(opts, client.WithHost(host))
	}

	if b.config.APIVersion != "" {
		opts = append(opts, client.WithVersion(b.config.APIVersion))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	b.client = cli
	return nil
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

	resp, err := b.client.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, string(containerID))
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
			hostConfig.PortBindings = make(nat.PortMap)
			for _, pm := range config.Network.PortMappings {
				protocol := pm.Protocol
				if protocol == "" {
					protocol = "tcp"
				}
				portKey, err := nat.NewPort(protocol, fmt.Sprintf("%d", pm.ContainerPort))
				if err != nil {
					return nil, nil, nil, fmt.Errorf("invalid port %d/%s: %w", pm.ContainerPort, protocol, err)
				}

				binding := nat.PortBinding{
					HostPort: fmt.Sprintf("%d", pm.HostPort),
				}
				if pm.HostIP != "" {
					binding.HostIP = pm.HostIP
				}

				hostConfig.PortBindings[portKey] = append(hostConfig.PortBindings[portKey], binding)
			}
		}

		if len(config.Network.DNS) > 0 {
			hostConfig.DNS = config.Network.DNS
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

	containers, err := b.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", string(id))),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	if len(containers) > 0 {
		b.containers[id] = containers[0].ID
		return containers[0].ID, nil
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

	if err := b.client.ContainerStart(ctx, dockerID, container.StartOptions{}); err != nil {
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
	if err := b.client.ContainerStop(ctx, dockerID, container.StopOptions{Timeout: &timeoutSeconds}); err != nil {
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

	if err := b.client.ContainerRemove(ctx, dockerID, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("failed to remove container %s: %w", id, err)
	}

	delete(b.containers, id)

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

	statusCh, errCh := b.client.ContainerWait(ctx, dockerID, container.WaitConditionNotRunning)

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("failed to wait for container %s: %w", id, err)
		}
	case status := <-statusCh:
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

	logs, err := b.client.ContainerLogs(ctx, dockerID, container.LogsOptions{
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

	info, err := b.client.ContainerInspect(ctx, dockerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s: %w", id, err)
	}

	containerInfo := &core.ContainerInfo{
		ID:     id,
		Image:  info.Config.Image,
		Status: info.State.Status,
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

	_, err := b.client.Ping(ctx)
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
