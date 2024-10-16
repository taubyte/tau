package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
)

func TestDoAuth_List(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Auth{Op: &pb.Auth_List{List: true}}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"main", "withkey"}, resp.Msg.GetSlice().GetValue())
}

func TestDoAuth_GetUsername(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
				Op: &pb.Signer_Username{
					Username: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "tau1", resp.Msg.GetString_())
}

func TestDoAuth_SetUsername(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
				Op: &pb.Signer_Username{
					Username: &pb.StringOp{
						Op: &pb.StringOp_Set{Set: "newuser"},
					},
				},
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
				Op: &pb.Signer_Username{
					Username: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "newuser", resp.Msg.GetString_())
}

func TestDoAuth_GetPassword(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
				Op: &pb.Signer_Password{
					Password: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "testtest", resp.Msg.GetString_())
}

func TestDoAuth_SetPassword(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
				Op: &pb.Signer_Password{
					Password: &pb.StringOp{
						Op: &pb.StringOp_Set{Set: "newpassword"},
					},
				},
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
				Op: &pb.Signer_Password{
					Password: &pb.StringOp{
						Op: &pb.StringOp_Get{Get: true},
					},
				},
			},
		},
	}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "newpassword", resp.Msg.GetString_())
}

func TestDoAuth_GetKeyPath(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "withkey",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						Op: &pb.SSHKey_Path{
							Path: &pb.StringOp{
								Op: &pb.StringOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "/keys/test.pem", resp.Msg.GetString_())
}

func TestDoAuth_SetKeyPath(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set new key path
	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "withkey",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						Op: &pb.SSHKey_Path{
							Path: &pb.StringOp{
								Op: &pb.StringOp_Set{Set: "/new/key/path.pem"},
							},
						},
					},
				},
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "withkey",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						Op: &pb.SSHKey_Path{
							Path: &pb.StringOp{
								Op: &pb.StringOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, "/new/key/path.pem", resp.Msg.GetString_())
}

func TestDoAuth_SetKeyData(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set new key data
	newKeyData := []byte("new key data")
	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "withkey",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						Op: &pb.SSHKey_Data{
							Data: &pb.BytesOp{
								Op: &pb.BytesOp_Set{Set: newKeyData},
							},
						},
					},
				},
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.NoError(t, err)

	// Verify the change
	in = &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "withkey",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						Op: &pb.SSHKey_Data{
							Data: &pb.BytesOp{
								Op: &pb.BytesOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.Equal(t, newKeyData, resp.Msg.GetBytes())
}

func TestDoAuth_DeleteSigner(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Confirm that "main" signer exists
	in := &pb.Auth{Op: &pb.Auth_List{List: true}}
	resp, err := service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.Contains(t, resp.Msg.GetSlice().GetValue(), "main")

	// Delete the "main" signer
	in = &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
				Op:   &pb.Signer_Delete{Delete: true},
			},
		},
	}
	_, err = service.doAuth(in, parser)
	assert.NoError(t, err)

	// Confirm that "main" signer no longer exists
	in = &pb.Auth{Op: &pb.Auth_List{List: true}}
	resp, err = service.doAuth(in, parser)
	assert.NoError(t, err)
	assert.NotContains(t, resp.Msg.GetSlice().GetValue(), "main")
}

func TestDoAuth_SelectNoName(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "",
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "signer must have a name", err.Error())
}

func TestDoAuth_InvalidAuthOperation(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	in := &pb.Auth{}
	_, err := service.doAuth(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "invalid auth operation", err.Error())
}

func TestDoAuth_InvalidAuthOperationForSigner(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Create a signer operation with no valid sub-operations
	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "invalid auth operation for main", err.Error())
}

func TestDoAuth_GetKeyData_NoKey(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Try to get key data for signer "main", which doesn't have a key
	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "main",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						Op: &pb.SSHKey_Data{
							Data: &pb.BytesOp{
								Op: &pb.BytesOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open ssh key for main")
}

func TestDoAuth_SetKeyData_InvalidKeyPath(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Set key path to an invalid location
	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "withkey",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						Op: &pb.SSHKey_Path{
							Path: &pb.StringOp{
								Op: &pb.StringOp_Set{Set: "/invalid/path.pem"},
							},
						},
					},
				},
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.NoError(t, err)

	// Try to get key data
	in = &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "withkey",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						Op: &pb.SSHKey_Data{
							Data: &pb.BytesOp{
								Op: &pb.BytesOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	}
	_, err = service.doAuth(in, parser)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open ssh key for")
}

func TestDoAuth_SetKey_Undefined(t *testing.T) {
	service := &Service{}
	_, parser := fixtures.VirtConfig()

	// Try to set key without specifying Path or Data
	in := &pb.Auth{
		Op: &pb.Auth_Select{
			Select: &pb.Signer{
				Name: "withkey",
				Op: &pb.Signer_Key{
					Key: &pb.SSHKey{
						// No Op specified
					},
				},
			},
		},
	}
	_, err := service.doAuth(in, parser)
	assert.Error(t, err)
	assert.Equal(t, "failed to set undefined ssh key", err.Error())
}
