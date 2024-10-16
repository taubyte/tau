package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
)

func TestDoCloud_GetRootDomain(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Root{
					Root: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}
	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "test.com", resp.Msg.GetString_())
}

func TestDoCloud_SetRootDomain(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set new root domain
	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Root{
					Root: &pb.StringOp{
						Op: &pb.StringOp_Set{Set: "newroot.com"},
					},
				},
			},
		},
	}
	_, err := service.doCloud(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Root{
					Root: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}
	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "newroot.com", resp.Msg.GetString_())
}

func TestDoCloud_GetGeneratedDomain(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Generated{
					Generated: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}
	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "gtest.com", resp.Msg.GetString_())
}

func TestDoCloud_SetGeneratedDomain(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Generated{
					Generated: &pb.StringOp{
						Op: &pb.StringOp_Set{Set: "newgenerated.com"},
					},
				},
			},
		},
	}
	_, err := service.doCloud(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Generated{
					Generated: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}
	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "newgenerated.com", resp.Msg.GetString_())
}

func TestDoCloud_DomainValidation_GenerateKeys(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Validation{
					Validation: &pb.Validation{
						Op: &pb.Validation_Generate{Generate: true},
					},
				},
			},
		},
	}
	_, err := service.doCloud(in, parser)
	assert.NoError(t, err)
}

func TestDoCloud_DomainValidation_GetPrivateKeyPath(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Validation{
					Validation: &pb.Validation{
						Op: &pb.Validation_Keys{
							Keys: &pb.ValidationKeys{
								Op: &pb.ValidationKeys_Path{
									Path: &pb.ValidationKeysPath{
										Op: &pb.ValidationKeysPath_PrivateKey{
											PrivateKey: &pb.StringOp{
												Op: &pb.StringOp_Get{Get: true},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.Msg.GetString_())
}

func TestDoCloud_DomainValidation_SetPrivateKeyPath(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	newPath := "/new/private/key/path.pem"
	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Validation{
					Validation: &pb.Validation{
						Op: &pb.Validation_Keys{
							Keys: &pb.ValidationKeys{
								Op: &pb.ValidationKeys_Path{
									Path: &pb.ValidationKeysPath{
										Op: &pb.ValidationKeysPath_PrivateKey{
											PrivateKey: &pb.StringOp{
												Op: &pb.StringOp_Set{Set: newPath},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doCloud(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Validation{
					Validation: &pb.Validation{
						Op: &pb.Validation_Keys{
							Keys: &pb.ValidationKeys{
								Op: &pb.ValidationKeys_Path{
									Path: &pb.ValidationKeysPath{
										Op: &pb.ValidationKeysPath_PrivateKey{
											PrivateKey: &pb.StringOp{
												Op: &pb.StringOp_Get{Get: true},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, newPath, resp.Msg.GetString_())
}

func TestDoCloud_DomainValidation_GetPrivateKeyData(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Get private key data
	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Validation{
					Validation: &pb.Validation{
						Op: &pb.Validation_Keys{
							Keys: &pb.ValidationKeys{
								Op: &pb.ValidationKeys_Data{
									Data: &pb.ValidationKeysData{
										Op: &pb.ValidationKeysData_PrivateKey{
											PrivateKey: &pb.BytesOp{
												Op: &pb.BytesOp_Get{Get: true},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.Msg.GetBytes())
}

func TestDoCloud_DomainValidation_SetPrivateKeyData(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	newKeyData := []byte("new private key data")

	// Set private key data
	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Validation{
					Validation: &pb.Validation{
						Op: &pb.Validation_Keys{
							Keys: &pb.ValidationKeys{
								Op: &pb.ValidationKeys_Data{
									Data: &pb.ValidationKeysData{
										Op: &pb.ValidationKeysData_PrivateKey{
											PrivateKey: &pb.BytesOp{
												Op: &pb.BytesOp_Set{Set: newKeyData},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doCloud(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Validation{
					Validation: &pb.Validation{
						Op: &pb.Validation_Keys{
							Keys: &pb.ValidationKeys{
								Op: &pb.ValidationKeys_Data{
									Data: &pb.ValidationKeysData{
										Op: &pb.ValidationKeysData_PrivateKey{
											PrivateKey: &pb.BytesOp{
												Op: &pb.BytesOp_Get{Get: true},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, newKeyData, resp.Msg.GetBytes())
}

func TestDoCloud_P2P_Bootstrap_List(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{
				Op: &pb.P2P_Bootstrap{
					Bootstrap: &pb.Bootstrap{
						Op: &pb.Bootstrap_List{List: true},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"shape1", "shape2"}, resp.Msg.GetSlice().GetValue())
}

func TestDoCloud_P2P_Bootstrap_Select_ListNodes(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{
				Op: &pb.P2P_Bootstrap{
					Bootstrap: &pb.Bootstrap{
						Op: &pb.Bootstrap_Select{
							Select: &pb.BootstrapShape{
								Shape: "shape1",
								Op: &pb.BootstrapShape_Nodes{
									Nodes: &pb.StringSliceOp{
										Op: &pb.StringSliceOp_List{List: true},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"host2", "host1"}, resp.Msg.GetSlice().GetValue())
}

func TestDoCloud_P2P_Bootstrap_Select_SetNodes(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	newNodes := []string{"host3", "host4"}

	in := &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{
				Op: &pb.P2P_Bootstrap{
					Bootstrap: &pb.Bootstrap{
						Op: &pb.Bootstrap_Select{
							Select: &pb.BootstrapShape{
								Shape: "shape1",
								Op: &pb.BootstrapShape_Nodes{
									Nodes: &pb.StringSliceOp{
										Op: &pb.StringSliceOp_Set{
											Set: &pb.StringSlice{Value: newNodes},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doCloud(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{
				Op: &pb.P2P_Bootstrap{
					Bootstrap: &pb.Bootstrap{
						Op: &pb.Bootstrap_Select{
							Select: &pb.BootstrapShape{
								Shape: "shape1",
								Op: &pb.BootstrapShape_Nodes{
									Nodes: &pb.StringSliceOp{
										Op: &pb.StringSliceOp_List{List: true},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, newNodes, resp.Msg.GetSlice().GetValue())
}

func TestDoCloud_P2P_Swarm_Generate(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{
				Op: &pb.P2P_Swarm{
					Swarm: &pb.Swarm{
						Op: &pb.Swarm_Generate{
							Generate: true,
						},
					},
				},
			},
		},
	}

	_, err := service.doCloud(in, parser)
	assert.NoError(t, err)
}

func TestDoCloud_P2P_Swarm_GetKeyPath(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{
				Op: &pb.P2P_Swarm{
					Swarm: &pb.Swarm{
						Op: &pb.Swarm_Key{
							Key: &pb.SwarmKey{
								Op: &pb.SwarmKey_Path{
									Path: &pb.StringOp{
										Op: &pb.StringOp_Get{Get: true},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.Msg.GetString_())
}

func TestDoCloud_P2P_Swarm_SetKeyPath(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	newPath := "/new/swarm/key/path.key"
	in := &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{
				Op: &pb.P2P_Swarm{
					Swarm: &pb.Swarm{
						Op: &pb.Swarm_Key{
							Key: &pb.SwarmKey{
								Op: &pb.SwarmKey_Path{
									Path: &pb.StringOp{
										Op: &pb.StringOp_Set{Set: newPath},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := service.doCloud(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{
				Op: &pb.P2P_Swarm{
					Swarm: &pb.Swarm{
						Op: &pb.Swarm_Key{
							Key: &pb.SwarmKey{
								Op: &pb.SwarmKey_Path{
									Path: &pb.StringOp{
										Op: &pb.StringOp_Get{Get: true},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := service.doCloud(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, newPath, resp.Msg.GetString_())
}

func TestDoCloud_InvalidOperation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Cloud{}
	_, err := service.doCloud(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "invalid cloud operation", err.Error())
}

func TestDoCloud_InvalidP2POperation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Invalid P2P operation without Op
	in := &pb.Cloud{
		Op: &pb.Cloud_P2P{
			P2P: &pb.P2P{},
		},
	}

	_, err := service.doCloud(in, parser)
	assert.Error(t, err)
}

func TestDoCloud_DomainValidation_InvalidKeysOperation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Invalid Keys operation without Op
	in := &pb.Cloud{
		Op: &pb.Cloud_Domain{
			Domain: &pb.Domain{
				Op: &pb.Domain_Validation{
					Validation: &pb.Validation{
						Op: &pb.Validation_Keys{
							Keys: &pb.ValidationKeys{},
						},
					},
				},
			},
		},
	}

	_, err := service.doCloud(in, parser)
	assert.Error(t, err)
}
