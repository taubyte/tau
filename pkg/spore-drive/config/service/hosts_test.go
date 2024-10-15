package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
)

func TestDoHosts_List(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Hosts{Op: &pb.Hosts_List{List: true}}
	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"host1", "host2"}, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_GetSSHAddress(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Ssh{
					Ssh: &pb.SSH{
						Op: &pb.SSH_Address{
							Address: &pb.StringOp{
								Op: &pb.StringOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}
	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "1.2.3.4:4242", resp.Msg.GetString_())
}

func TestDoHosts_SetSSHAddress(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set new SSH address
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Ssh{
					Ssh: &pb.SSH{
						Op: &pb.SSH_Address{
							Address: &pb.StringOp{
								Op: &pb.StringOp_Set{Set: "5.6.7.8:2222"},
							},
						},
					},
				},
			},
		},
	}
	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Ssh{
					Ssh: &pb.SSH{
						Op: &pb.SSH_Address{
							Address: &pb.StringOp{
								Op: &pb.StringOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}
	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "5.6.7.8:2222", resp.Msg.GetString_())
}

func TestDoHosts_GetAddresses(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// List addresses of host1
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"1.2.3.4/24", "4.3.2.1/24"}, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_SetAddresses(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set new addresses for host1
	newAddresses := []string{"10.0.0.1/24", "10.0.0.2/24"}
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_Set{
							Set: &pb.StringSlice{Value: newAddresses},
						},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, newAddresses, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_AddAddresses(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Add new addresses to host1
	additionalAddresses := []string{"10.0.0.3/24"}
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_Add{
							Add: &pb.StringSlice{Value: additionalAddresses},
						},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	expectedAddresses := []string{"1.2.3.4/24", "4.3.2.1/24", "10.0.0.3/24"}
	assert.ElementsMatch(t, expectedAddresses, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_DeleteAddresses(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Delete an address from host1
	addressToDelete := []string{"1.2.3.4/24"}
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_Delete{
							Delete: &pb.StringSlice{Value: addressToDelete},
						},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	expectedAddresses := []string{"4.3.2.1/24"}
	assert.ElementsMatch(t, expectedAddresses, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_ClearAddresses(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Clear all addresses from host1
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_Clear{Clear: true},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify that addresses are cleared
	in = &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Addresses{
					Addresses: &pb.StringSliceOp{
						Op: &pb.StringSliceOp_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.Empty(t, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_GetSSHAuthList(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Get SSH auth list for host1
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Ssh{
					Ssh: &pb.SSH{
						Op: &pb.SSH_Auth{
							Auth: &pb.StringSliceOp{
								Op: &pb.StringSliceOp_List{List: true},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"main"}, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_SetSSHAuth(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set SSH auth for host1
	newAuth := []string{"withkey"}
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Ssh{
					Ssh: &pb.SSH{
						Op: &pb.SSH_Auth{
							Auth: &pb.StringSliceOp{
								Op: &pb.StringSliceOp_Set{
									Set: &pb.StringSlice{Value: newAuth},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Ssh{
					Ssh: &pb.SSH{
						Op: &pb.SSH_Auth{
							Auth: &pb.StringSliceOp{
								Op: &pb.StringSliceOp_List{List: true},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, newAuth, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_GetLocation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Get location of host1
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Location{
					Location: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "1.250000,25.100000", resp.Msg.GetString_())
}

func TestDoHosts_SetLocation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set new location for host1
	newLocation := "2.5,30.0"
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Location{
					Location: &pb.StringOp{
						Op: &pb.StringOp_Set{Set: newLocation},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Location{
					Location: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "2.500000,30.000000", resp.Msg.GetString_())
}

func TestDoHosts_GetShapes(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// List shapes of host1
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Shapes{
					Shapes: &pb.HostShapes{
						Op: &pb.HostShapes_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"shape1", "shape2"}, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_DeleteShape(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Delete shape1 from host1
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Shapes{
					Shapes: &pb.HostShapes{
						Op: &pb.HostShapes_Select{
							Select: &pb.HostShape{
								Name: "shape1",
								Op: &pb.HostShape_Delete{
									Delete: true,
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify the shape is deleted
	in = &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Shapes{
					Shapes: &pb.HostShapes{
						Op: &pb.HostShapes_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"shape2"}, resp.Msg.GetSlice().GetValue())
}

func TestDoHosts_DeleteHost(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Delete host1
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Delete{
					Delete: true,
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.NoError(t, err)

	// Verify the host is deleted
	in = &pb.Hosts{Op: &pb.Hosts_List{List: true}}
	resp, err := service.doHosts(in, parser)
	assert.NoError(t, err)
	assert.NotContains(t, resp.Msg.GetSlice().GetValue(), "host1")
}

func TestDoHosts_InvalidOperation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Invalid operation without Op
	in := &pb.Hosts{}
	_, err := service.doHosts(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "invalid host operation", err.Error())
}

func TestDoHosts_SelectNoName(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Select host without name
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid host operation")
}

func TestDoHosts_InvalidHostOperation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Invalid host operation with no Op
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "invalid host operation", err.Error())
}

func TestDoHosts_SetLocation_InvalidFormat(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set location with invalid format
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "host1",
				Op: &pb.Host_Location{
					Location: &pb.StringOp{
						Op: &pb.StringOp_Set{Set: "invalid,location"},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "invalid location format: expected `latitude,longitude`", err.Error())
}

func TestDoHosts_GetNonExistentHost(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Try to get SSH address of a non-existent host
	in := &pb.Hosts{
		Op: &pb.Hosts_Select{
			Select: &pb.Host{
				Name: "nonexistent",
				Op: &pb.Host_Ssh{
					Ssh: &pb.SSH{
						Op: &pb.SSH_Address{
							Address: &pb.StringOp{
								Op: &pb.StringOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doHosts(in, parser)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "host not found")
}
