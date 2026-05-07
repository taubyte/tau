package service

import (
	"errors"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"

	"github.com/taubyte/tau/pkg/spore-drive/config"
)

func (s *Service) doAccounts(in *pb.Accounts, p config.Parser) (*connect.Response[pb.Return], error) {
	if x := in.GetSessionTtl(); x != nil {
		if x.GetGet() {
			return returnString(p.Accounts().SessionTTL()), nil
		}
		return returnEmpty(p.Accounts().SetSessionTTL(x.GetSet()))
	}

	if e := in.GetEmail(); e != nil {
		return s.doAccountsEmail(e, p)
	}

	return nil, errors.New("invalid accounts operation")
}

func (s *Service) doAccountsEmail(in *pb.AccountsEmail, p config.Parser) (*connect.Response[pb.Return], error) {
	if x := in.GetSmtp(); x != nil {
		return s.doAccountsSMTP(x, p)
	}
	return nil, errors.New("invalid accounts.email operation")
}

func (s *Service) doAccountsSMTP(in *pb.SMTP, p config.Parser) (*connect.Response[pb.Return], error) {
	smtp := p.Accounts().Email().SMTP()

	if x := in.GetHost(); x != nil {
		if x.GetGet() {
			return returnString(smtp.Host()), nil
		}
		return returnEmpty(smtp.SetHost(x.GetSet()))
	}
	if x := in.GetPort(); x != nil {
		if x.GetGet() {
			return returnUint(smtp.Port()), nil
		}
		return returnEmpty(smtp.SetPort(x.GetSet()))
	}
	if x := in.GetUser(); x != nil {
		if x.GetGet() {
			return returnString(smtp.User()), nil
		}
		return returnEmpty(smtp.SetUser(x.GetSet()))
	}
	if x := in.GetPass(); x != nil {
		if x.GetGet() {
			return returnString(smtp.Pass()), nil
		}
		return returnEmpty(smtp.SetPass(x.GetSet()))
	}
	if x := in.GetFrom(); x != nil {
		if x.GetGet() {
			return returnString(smtp.From()), nil
		}
		return returnEmpty(smtp.SetFrom(x.GetSet()))
	}

	return nil, errors.New("invalid accounts.email.smtp operation")
}
