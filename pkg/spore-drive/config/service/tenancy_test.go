package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
)

// --- provider / owner ----------------------------------------------------

func TestDoTenancy_GetOwner(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Tenancy{
		Op: &pb.Tenancy_Owner{
			Owner: &pb.StringOp{Op: &pb.StringOp_Get{Get: true}},
		},
	}
	resp, err := service.doTenancy(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "taubyte", resp.Msg.GetString_())
}

func TestDoTenancy_SetOwner(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Tenancy{
		Op: &pb.Tenancy_Owner{
			Owner: &pb.StringOp{Op: &pb.StringOp_Set{Set: "acme"}},
		},
	}
	_, err := service.doTenancy(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "acme", p.Tenancy().Owner())
}

func TestDoTenancy_GetProvider(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Tenancy{
		Op: &pb.Tenancy_Provider{
			Provider: &pb.StringOp{Op: &pb.StringOp_Get{Get: true}},
		},
	}
	resp, err := service.doTenancy(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "github", resp.Msg.GetString_())
}

func TestDoTenancy_SetProvider(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Tenancy{
		Op: &pb.Tenancy_Provider{
			Provider: &pb.StringOp{Op: &pb.StringOp_Set{Set: "gitlab"}},
		},
	}
	_, err := service.doTenancy(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "gitlab", p.Tenancy().Provider())
}

// --- app.{client_id,key} -------------------------------------------------

func TestDoTenancy_GetAppClientId(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Tenancy{
		Op: &pb.Tenancy_App{
			App: &pb.TenancyApp{
				Op: &pb.TenancyApp_ClientId{
					ClientId: &pb.StringOp{Op: &pb.StringOp_Get{Get: true}},
				},
			},
		},
	}
	resp, err := service.doTenancy(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "Iv1.0000000000000000", resp.Msg.GetString_())
}

func TestDoTenancy_SetAppKey(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	pem := "-----BEGIN RSA PRIVATE KEY-----\nrotated\n-----END RSA PRIVATE KEY-----\n"
	in := &pb.Tenancy{
		Op: &pb.Tenancy_App{
			App: &pb.TenancyApp{
				Op: &pb.TenancyApp_Key{
					Key: &pb.StringOp{Op: &pb.StringOp_Set{Set: pem}},
				},
			},
		},
	}
	_, err := service.doTenancy(in, p)
	assert.NoError(t, err)
	assert.Equal(t, pem, p.Tenancy().App().Key())
}

func TestDoTenancy_InvalidOp(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	_, err := service.doTenancy(&pb.Tenancy{}, p)
	assert.Error(t, err, "invalid tenancy operation")

	_, err = service.doTenancy(&pb.Tenancy{
		Op: &pb.Tenancy_App{App: &pb.TenancyApp{}},
	}, p)
	assert.Error(t, err, "invalid tenancy.app operation")
}
