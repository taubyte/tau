package config

type AccountsParser interface {
	SessionTTL() string
	SetSessionTTL(string) error
	Email() EmailParser
}

type EmailParser interface {
	SMTP() SMTPParser
}

type SMTPParser interface {
	Host() string
	Port() uint64
	User() string
	Pass() string
	From() string

	SetHost(string) error
	SetPort(uint64) error
	SetUser(string) error
	SetPass(string) error
	SetFrom(string) error
}

type (
	accounts leaf
	email    leaf
	smtp     leaf
)

func (a *accounts) SessionTTL() (v string) {
	a.Fork().Get("session-ttl").Value(&v)
	return
}

func (a *accounts) SetSessionTTL(v string) error {
	return a.Fork().Get("session-ttl").Set(v).Commit()
}

func (a *accounts) Email() EmailParser {
	return &email{root: a.root, Query: a.Fork().Get("email")}
}

func (e *email) SMTP() SMTPParser {
	return &smtp{root: e.root, Query: e.Fork().Get("smtp")}
}

func (s *smtp) Host() (v string) { s.Fork().Get("host").Value(&v); return }
func (s *smtp) Port() (v uint64) { s.Fork().Get("port").Value(&v); return }
func (s *smtp) User() (v string) { s.Fork().Get("user").Value(&v); return }
func (s *smtp) Pass() (v string) { s.Fork().Get("pass").Value(&v); return }
func (s *smtp) From() (v string) { s.Fork().Get("from").Value(&v); return }

func (s *smtp) SetHost(v string) error { return s.Fork().Get("host").Set(v).Commit() }
func (s *smtp) SetPort(v uint64) error { return s.Fork().Get("port").Set(v).Commit() }
func (s *smtp) SetUser(v string) error { return s.Fork().Get("user").Set(v).Commit() }
func (s *smtp) SetPass(v string) error { return s.Fork().Get("pass").Set(v).Commit() }
func (s *smtp) SetFrom(v string) error { return s.Fork().Get("from").Set(v).Commit() }
