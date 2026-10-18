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
	a.Query.Get("session-ttl").Value(&v)
	return
}

func (a *accounts) SetSessionTTL(v string) error {
	return a.Query.Get("session-ttl").Set(v).Commit()
}

func (a *accounts) Email() EmailParser {
	return &email{root: a.root, Query: a.Query.Get("email")}
}

func (e *email) SMTP() SMTPParser {
	return &smtp{root: e.root, Query: e.Query.Get("smtp")}
}

func (s *smtp) Host() (v string) { s.Query.Get("host").Value(&v); return }
func (s *smtp) Port() (v uint64) { s.Query.Get("port").Value(&v); return }
func (s *smtp) User() (v string) { s.Query.Get("user").Value(&v); return }
func (s *smtp) Pass() (v string) { s.Query.Get("pass").Value(&v); return }
func (s *smtp) From() (v string) { s.Query.Get("from").Value(&v); return }

func (s *smtp) SetHost(v string) error { return s.Query.Get("host").Set(v).Commit() }
func (s *smtp) SetPort(v uint64) error { return s.Query.Get("port").Set(v).Commit() }
func (s *smtp) SetUser(v string) error { return s.Query.Get("user").Set(v).Commit() }
func (s *smtp) SetPass(v string) error { return s.Query.Get("pass").Set(v).Commit() }
func (s *smtp) SetFrom(v string) error { return s.Query.Get("from").Set(v).Commit() }
