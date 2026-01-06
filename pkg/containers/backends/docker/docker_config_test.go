package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestCreateDockerConfig(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		backend := &DockerBackend{}

		config := &core.ContainerConfig{
			Image:   "alpine:latest",
			Command: []string{"echo", "hello"},
			Env:     []string{"FOO=bar"},
			WorkDir: "/tmp",
		}

		containerConfig, hostConfig, networkingConfig, err := backend.createDockerConfig(config)
		require.NoError(t, err, "createDockerConfig should succeed")

		assert.Equal(t, "alpine:latest", containerConfig.Image)
		assert.Equal(t, []string{"echo", "hello"}, []string(containerConfig.Cmd))
		assert.Equal(t, []string{"FOO=bar"}, containerConfig.Env)
		assert.Equal(t, "/tmp", containerConfig.WorkingDir)
		assert.NotNil(t, hostConfig)
		assert.NotNil(t, networkingConfig)
	})

	t.Run("Command", func(t *testing.T) {
		backend := &DockerBackend{}

		t.Run("Nil", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: nil,
			}

			containerConfig, _, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Nil(t, containerConfig.Cmd, "Cmd must be nil when Command is nil")
		})

		t.Run("Empty", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: []string{},
			}

			containerConfig, _, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Empty(t, containerConfig.Cmd, "Cmd must be empty when Command is empty")
		})
	})

	t.Run("Environment", func(t *testing.T) {
		backend := &DockerBackend{}

		t.Run("Default", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: []string{"echo", "hello"},
			}

			containerConfig, _, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Contains(t, containerConfig.Env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
		})

		t.Run("Empty", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Env:   []string{},
			}

			containerConfig, _, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Contains(t, containerConfig.Env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "Default PATH must be set")
		})

		t.Run("Provided", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: []string{"echo", "test"},
				Env:     []string{"FOO=bar", "BAZ=qux"},
			}

			containerConfig, _, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Equal(t, []string{"FOO=bar", "BAZ=qux"}, containerConfig.Env, "Env must match provided values")
		})
	})

	t.Run("WorkDir", func(t *testing.T) {
		backend := &DockerBackend{}

		t.Run("Default", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: []string{"echo", "hello"},
			}

			containerConfig, _, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Equal(t, "/", containerConfig.WorkingDir)
		})

		t.Run("Set", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				WorkDir: "/custom/workdir",
			}

			containerConfig, _, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Equal(t, "/custom/workdir", containerConfig.WorkingDir, "WorkingDir must be set correctly")
		})

		t.Run("Provided", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				WorkDir: "/custom/path",
			}

			containerConfig, _, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Equal(t, "/custom/path", containerConfig.WorkingDir, "WorkingDir must match provided value")
		})
	})

	t.Run("Resources", func(t *testing.T) {
		backend := &DockerBackend{}

		t.Run("Nil", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:     "alpine:latest",
				Resources: nil,
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Equal(t, container.Resources{}, hostConfig.Resources, "Resources must be empty when nil")
		})

		t.Run("AllLimits", func(t *testing.T) {
			memory := int64(1024 * 1024 * 512)
			memorySwap := int64(1024 * 1024 * 1024)
			cpuQuota := int64(50000)
			cpuPeriod := int64(100000)
			cpuShares := int64(512)
			pids := int64(100)

			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Resources: &core.ResourceLimits{
					Memory:     memory,
					MemorySwap: memorySwap,
					CPUQuota:   cpuQuota,
					CPUPeriod:  cpuPeriod,
					CPUShares:  cpuShares,
					PIDs:       pids,
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			assert.Equal(t, memory, hostConfig.Resources.Memory)
			assert.Equal(t, memorySwap, hostConfig.Resources.MemorySwap)
			assert.Equal(t, cpuQuota, hostConfig.Resources.CPUQuota)
			assert.Equal(t, cpuPeriod, hostConfig.Resources.CPUPeriod)
			assert.Equal(t, cpuShares, hostConfig.Resources.CPUShares)
			assert.NotNil(t, hostConfig.Resources.PidsLimit)
			assert.Equal(t, pids, *hostConfig.Resources.PidsLimit)
		})

		t.Run("ZeroValues", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Resources: &core.ResourceLimits{
					Memory:     0,
					MemorySwap: 0,
					CPUQuota:   0,
					CPUPeriod:  0,
					CPUShares:  0,
					PIDs:       0,
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			assert.Equal(t, int64(0), hostConfig.Resources.Memory)
			assert.Nil(t, hostConfig.Resources.PidsLimit, "PidsLimit must be nil when PIDs is 0")
		})

		t.Run("MemoryOnly", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Resources: &core.ResourceLimits{
					Memory: 1024 * 1024 * 512,
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			assert.Equal(t, int64(1024*1024*512), hostConfig.Resources.Memory)
			assert.Equal(t, int64(0), hostConfig.Resources.MemorySwap)
		})

		t.Run("MemoryZero", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Resources: &core.ResourceLimits{
					Memory: 0,
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Equal(t, int64(0), hostConfig.Resources.Memory, "Memory must be 0 when not set")
		})

		t.Run("MemorySwap", func(t *testing.T) {
			t.Run("Positive", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						Memory:     1024 * 1024 * 512,
						MemorySwap: 1024 * 1024 * 1024,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)

				assert.Equal(t, int64(1024*1024*512), hostConfig.Resources.Memory)
				assert.Equal(t, int64(1024*1024*1024), hostConfig.Resources.MemorySwap)
			})

			t.Run("Unlimited", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						Memory:     1024 * 1024 * 512,
						MemorySwap: -1,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, int64(-1), hostConfig.Resources.MemorySwap, "MemorySwap must be -1 for unlimited")
			})

			t.Run("Zero", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						MemorySwap: 0,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, int64(0), hostConfig.Resources.MemorySwap, "MemorySwap must be 0 when not set")
			})
		})

		t.Run("CPU", func(t *testing.T) {
			t.Run("QuotaAndPeriod", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						CPUQuota:  50000,
						CPUPeriod: 100000,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)

				assert.Equal(t, int64(50000), hostConfig.Resources.CPUQuota)
				assert.Equal(t, int64(100000), hostConfig.Resources.CPUPeriod)
			})

			t.Run("Shares", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						CPUShares: 512,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, int64(512), hostConfig.Resources.CPUShares)
			})

			t.Run("Zero", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						CPUQuota:  0,
						CPUPeriod: 0,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, int64(0), hostConfig.Resources.CPUQuota, "CPUQuota must be 0 when not set")
				assert.Equal(t, int64(0), hostConfig.Resources.CPUPeriod, "CPUPeriod must be 0 when not set")
			})

			t.Run("SharesZero", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						CPUShares: 0,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, int64(0), hostConfig.Resources.CPUShares, "CPUShares must be 0 when not set")
			})
		})

		t.Run("PIDs", func(t *testing.T) {
			t.Run("WithLimit", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						Memory: 1024 * 1024 * 512,
						PIDs:   100,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.NotNil(t, hostConfig.Resources.PidsLimit)
				assert.Equal(t, int64(100), *hostConfig.Resources.PidsLimit)
			})

			t.Run("WithoutLimit", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Resources: &core.ResourceLimits{
						Memory: 1024 * 1024 * 512,
						PIDs:   0,
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Nil(t, hostConfig.Resources.PidsLimit, "PidsLimit must be nil when PIDs is 0")
			})
		})
	})

	t.Run("Volumes", func(t *testing.T) {
		backend := &DockerBackend{}

		t.Run("Empty", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: []string{"echo", "test"},
				Volumes: []core.VolumeMount{},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)
			assert.Nil(t, hostConfig.Mounts, "Mounts must be nil when no volumes")
		})

		t.Run("BindMount", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Volumes: []core.VolumeMount{
					{
						Source:      "/host/path",
						Destination: "/container/path",
						ReadOnly:    true,
					},
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			require.Len(t, hostConfig.Mounts, 1, "Should have exactly 1 mount")
			assert.Equal(t, "bind", string(hostConfig.Mounts[0].Type), "First mount should be bind type")
			assert.Equal(t, "/host/path", hostConfig.Mounts[0].Source, "First mount source should match")
			assert.Equal(t, "/container/path", hostConfig.Mounts[0].Target, "First mount target should match")
			assert.True(t, hostConfig.Mounts[0].ReadOnly, "First mount should be read-only")
		})

		t.Run("NamedVolume", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Volumes: []core.VolumeMount{
					{
						Source:       "volume-name",
						Destination:  "/container/volume",
						IsVolumeName: true,
					},
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			require.Len(t, hostConfig.Mounts, 1, "Should have exactly 1 mount")
			assert.Equal(t, "volume", string(hostConfig.Mounts[0].Type), "Mount should be volume type")
			assert.Equal(t, "volume-name", hostConfig.Mounts[0].Source, "Mount source should match")
			assert.Equal(t, "/container/volume", hostConfig.Mounts[0].Target, "Mount target should match")
		})

		t.Run("MixedTypes", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Volumes: []core.VolumeMount{
					{
						Source:       "/host/path1",
						Destination:  "/container/path1",
						ReadOnly:     true,
						IsVolumeName: false,
					},
					{
						Source:       "volume-name",
						Destination:  "/container/path2",
						ReadOnly:     false,
						IsVolumeName: true,
					},
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			require.Len(t, hostConfig.Mounts, 2, "Must have exactly 2 mounts")
			assert.Equal(t, "bind", string(hostConfig.Mounts[0].Type), "First mount must be bind")
			assert.Equal(t, "volume", string(hostConfig.Mounts[1].Type), "Second mount must be volume")
			assert.True(t, hostConfig.Mounts[0].ReadOnly, "First mount must be read-only")
			assert.False(t, hostConfig.Mounts[1].ReadOnly, "Second mount must not be read-only")
		})

		t.Run("ReadWrite", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Volumes: []core.VolumeMount{
					{
						Source:      "/host/path",
						Destination: "/container/path",
						ReadOnly:    false,
					},
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			require.Len(t, hostConfig.Mounts, 1, "Must have exactly 1 mount")
			assert.False(t, hostConfig.Mounts[0].ReadOnly, "Mount must not be read-only")
		})
	})

	t.Run("Network", func(t *testing.T) {
		backend := &DockerBackend{}

		t.Run("Nil", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image:   "alpine:latest",
				Command: []string{"echo", "test"},
				Network: nil,
			}

			_, hostConfig, networkingConfig, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			assert.NotNil(t, networkingConfig, "NetworkingConfig must not be nil")
			assert.Empty(t, hostConfig.DNS, "DNS must be empty when Network is nil")
		})

		t.Run("Mode", func(t *testing.T) {
			t.Run("Host", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						Mode: "host",
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, "host", string(hostConfig.NetworkMode))
			})

			t.Run("Bridge", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						Mode: "bridge",
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, "bridge", string(hostConfig.NetworkMode), "NetworkMode must be set correctly")
			})
		})

		t.Run("DNS", func(t *testing.T) {
			t.Run("Single", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						DNS: []string{"8.8.8.8"},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, []string{"8.8.8.8"}, hostConfig.DNS)
				assert.Nil(t, hostConfig.PortBindings, "PortBindings must be nil when no port mappings")
			})

			t.Run("Multiple", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						DNS: []string{"8.8.8.8", "8.8.4.4"},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, []string{"8.8.8.8", "8.8.4.4"}, hostConfig.DNS)
			})

			t.Run("WithMode", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						Mode: "host",
						DNS:  []string{"8.8.8.8", "1.1.1.1"},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				assert.Equal(t, "host", string(hostConfig.NetworkMode), "NetworkMode must be set")
				assert.Equal(t, []string{"8.8.8.8", "1.1.1.1"}, hostConfig.DNS, "DNS must be set")
			})
		})

		t.Run("PortMappings", func(t *testing.T) {
			t.Run("Basic", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						PortMappings: []core.PortMapping{
							{
								HostPort:      8080,
								ContainerPort: 80,
								Protocol:      "tcp",
								HostIP:        "127.0.0.1",
							},
						},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				require.NotNil(t, hostConfig.PortBindings, "PortBindings should not be nil")
				assert.Greater(t, len(hostConfig.PortBindings), 0, "PortBindings should contain mappings")
			})

			t.Run("WithoutHostIP", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						PortMappings: []core.PortMapping{
							{
								HostPort:      8080,
								ContainerPort: 80,
								Protocol:      "tcp",
								HostIP:        "",
							},
						},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				require.NotNil(t, hostConfig.PortBindings, "PortBindings must not be nil")
				assert.Greater(t, len(hostConfig.PortBindings), 0, "PortBindings must contain mappings")
			})

			t.Run("DefaultProtocol", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						PortMappings: []core.PortMapping{
							{
								HostPort:      8080,
								ContainerPort: 80,
								Protocol:      "",
							},
						},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				require.NotNil(t, hostConfig.PortBindings, "PortBindings must not be nil")
				assert.Greater(t, len(hostConfig.PortBindings), 0, "PortBindings must contain mappings")
			})

			t.Run("UDP", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						PortMappings: []core.PortMapping{
							{
								HostPort:      53,
								ContainerPort: 53,
								Protocol:      "udp",
							},
						},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				require.NotNil(t, hostConfig.PortBindings, "PortBindings must not be nil")
				assert.Greater(t, len(hostConfig.PortBindings), 0, "PortBindings must contain mappings")
			})

			t.Run("Multiple", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						PortMappings: []core.PortMapping{
							{
								HostPort:      8080,
								ContainerPort: 80,
								Protocol:      "tcp",
							},
							{
								HostPort:      8443,
								ContainerPort: 443,
								Protocol:      "tcp",
							},
						},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				require.NotNil(t, hostConfig.PortBindings, "PortBindings must not be nil")
				assert.GreaterOrEqual(t, len(hostConfig.PortBindings), 2, "PortBindings must contain at least 2 mappings")
			})

			t.Run("WithHostIP", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						PortMappings: []core.PortMapping{
							{
								HostPort:      8080,
								ContainerPort: 80,
								Protocol:      "tcp",
								HostIP:        "127.0.0.1",
							},
						},
					},
				}

				_, hostConfig, _, err := backend.createDockerConfig(config)
				require.NoError(t, err)
				require.NotNil(t, hostConfig.PortBindings, "PortBindings must not be nil")
				assert.Greater(t, len(hostConfig.PortBindings), 0, "PortBindings must contain mappings")
			})

			t.Run("InvalidPort", func(t *testing.T) {
				config := &core.ContainerConfig{
					Image: "alpine:latest",
					Network: &core.NetworkConfig{
						PortMappings: []core.PortMapping{
							{
								HostPort:      8080,
								ContainerPort: 99999,
								Protocol:      "invalid-protocol",
							},
						},
					},
				}

				_, _, _, err := backend.createDockerConfig(config)
				assert.Error(t, err, "createDockerConfig must fail for invalid port")
				assert.Contains(t, err.Error(), "invalid port")
			})
		})

		t.Run("AllOptions", func(t *testing.T) {
			config := &core.ContainerConfig{
				Image: "alpine:latest",
				Network: &core.NetworkConfig{
					Mode: "bridge",
					PortMappings: []core.PortMapping{
						{
							HostPort:      8080,
							ContainerPort: 80,
							Protocol:      "tcp",
							HostIP:        "0.0.0.0",
						},
					},
					DNS: []string{"8.8.8.8"},
				},
			}

			_, hostConfig, _, err := backend.createDockerConfig(config)
			require.NoError(t, err)

			assert.Equal(t, "bridge", string(hostConfig.NetworkMode), "NetworkMode must be set")
			assert.NotNil(t, hostConfig.PortBindings, "PortBindings must not be nil")
			assert.Equal(t, []string{"8.8.8.8"}, hostConfig.DNS, "DNS must be set")
		})
	})
}
