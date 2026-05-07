package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
)

// --- session_ttl ---------------------------------------------------------

func TestDoAccounts_GetSessionTTL(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Accounts{
		Op: &pb.Accounts_SessionTtl{
			SessionTtl: &pb.StringOp{Op: &pb.StringOp_Get{Get: true}},
		},
	}
	resp, err := service.doAccounts(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "168h", resp.Msg.GetString_())
}

func TestDoAccounts_SetSessionTTL(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Accounts{
		Op: &pb.Accounts_SessionTtl{
			SessionTtl: &pb.StringOp{Op: &pb.StringOp_Set{Set: "12h"}},
		},
	}
	_, err := service.doAccounts(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "12h", p.Accounts().SessionTTL())
}

// --- email.smtp.{host,port,user,pass,from} -------------------------------

func TestDoAccounts_GetSMTPHost(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Accounts{
		Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{
			Op: &pb.AccountsEmail_Smtp{Smtp: &pb.SMTP{
				Op: &pb.SMTP_Host{Host: &pb.StringOp{Op: &pb.StringOp_Get{Get: true}}},
			}},
		}},
	}
	resp, err := service.doAccounts(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "smtp.example.com", resp.Msg.GetString_())
}

func TestDoAccounts_SetSMTPHost(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Accounts{
		Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{
			Op: &pb.AccountsEmail_Smtp{Smtp: &pb.SMTP{
				Op: &pb.SMTP_Host{Host: &pb.StringOp{Op: &pb.StringOp_Set{Set: "smtp2.example.com"}}},
			}},
		}},
	}
	_, err := service.doAccounts(in, p)
	assert.NoError(t, err)
	assert.Equal(t, "smtp2.example.com", p.Accounts().Email().SMTP().Host())
}

func TestDoAccounts_GetSMTPPort(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Accounts{
		Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{
			Op: &pb.AccountsEmail_Smtp{Smtp: &pb.SMTP{
				Op: &pb.SMTP_Port{Port: &pb.UInt64Op{Op: &pb.UInt64Op_Get{Get: true}}},
			}},
		}},
	}
	resp, err := service.doAccounts(in, p)
	assert.NoError(t, err)
	assert.Equal(t, uint64(587), resp.Msg.GetUint64())
}

func TestDoAccounts_SetSMTPPort(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Accounts{
		Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{
			Op: &pb.AccountsEmail_Smtp{Smtp: &pb.SMTP{
				Op: &pb.SMTP_Port{Port: &pb.UInt64Op{Op: &pb.UInt64Op_Set{Set: 2525}}},
			}},
		}},
	}
	_, err := service.doAccounts(in, p)
	assert.NoError(t, err)
	assert.Equal(t, uint64(2525), p.Accounts().Email().SMTP().Port())
}

func TestDoAccounts_SetThenGetSMTPUser(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	set := &pb.Accounts{Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{
		Op: &pb.AccountsEmail_Smtp{Smtp: &pb.SMTP{
			Op: &pb.SMTP_User{User: &pb.StringOp{Op: &pb.StringOp_Set{Set: "alice@example.com"}}},
		}},
	}}}
	_, err := service.doAccounts(set, p)
	assert.NoError(t, err)
	assert.Equal(t, "alice@example.com", p.Accounts().Email().SMTP().User())
}

func TestDoAccounts_SetThenGetSMTPPass(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	set := &pb.Accounts{Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{
		Op: &pb.AccountsEmail_Smtp{Smtp: &pb.SMTP{
			Op: &pb.SMTP_Pass{Pass: &pb.StringOp{Op: &pb.StringOp_Set{Set: "supersecret"}}},
		}},
	}}}
	_, err := service.doAccounts(set, p)
	assert.NoError(t, err)
	assert.Equal(t, "supersecret", p.Accounts().Email().SMTP().Pass())
}

func TestDoAccounts_SetThenGetSMTPFrom(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	set := &pb.Accounts{Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{
		Op: &pb.AccountsEmail_Smtp{Smtp: &pb.SMTP{
			Op: &pb.SMTP_From{From: &pb.StringOp{Op: &pb.StringOp_Set{Set: "no-reply@taubyte.com"}}},
		}},
	}}}
	_, err := service.doAccounts(set, p)
	assert.NoError(t, err)
	assert.Equal(t, "no-reply@taubyte.com", p.Accounts().Email().SMTP().From())
}

// --- error paths ---------------------------------------------------------

func TestDoAccounts_InvalidOperation(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	_, err := service.doAccounts(&pb.Accounts{}, p)
	assert.Error(t, err)
}

func TestDoAccounts_InvalidEmailOperation(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Accounts{Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{}}}
	_, err := service.doAccounts(in, p)
	assert.Error(t, err)
}

func TestDoAccounts_InvalidSMTPOperation(t *testing.T) {
	service := &Service{}
	_, p := fixtures.VirtConfig()

	in := &pb.Accounts{Op: &pb.Accounts_Email{Email: &pb.AccountsEmail{
		Op: &pb.AccountsEmail_Smtp{Smtp: &pb.SMTP{}},
	}}}
	_, err := service.doAccounts(in, p)
	assert.Error(t, err)
}
