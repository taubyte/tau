package config

import (
	"net"
	"strconv"
)

type HostsParser interface {
	List() []string
	Host(string) HostParser
	Delete(string) error
}

type HostParser interface {
	Addresses() ListParser[string]
	SSH() SSHParser
	Location() (float32, float32)
	SetLocation(float32, float32) error
	Shapes() HostShapesParser
}

type HostShapesParser interface {
	List() []string
	Instance(string) InstanceParser
	Delete(string) error
}

type InstanceParser interface {
	Key() string
	Id() string

	SetKey(string) error // will also set ID
	GenerateKey() error  // will also set ID
}

type SSHParser interface {
	Address() string
	Port() uint16

	SetAddress(string) error
	SetPort(uint16) error

	SetFullAddress(string) error

	Auth() ListParser[string]
}

type (
	hosts      leaf
	host       leaf
	ssh        leaf
	hostShapes leaf
	instShape  leaf
)

type location struct {
	Latitude  float32 `yaml:"lat"`
	Longitude float32 `yaml:"long"`
}

func (h *hosts) List() []string {
	l, _ := h.Fork().List()
	return l
}

func (h *hosts) Host(name string) HostParser {
	return &host{root: h.root, Query: h.Fork().Get(name)}
}

func (h *hosts) Delete(name string) error {
	return h.Fork().Get(name).Delete().Commit()
}

func (h *host) Addresses() ListParser[string] {
	return &list[string]{root: h.root, Query: h.Fork().Get("addr")}
}

func (h *host) SSH() SSHParser {
	return &ssh{root: h.root, Query: h.Fork().Get("ssh")}
}

func (h *host) Location() (float32, float32) {
	var l location
	h.Fork().Get("location").Value(&l)
	return l.Latitude, l.Longitude
}

func (h *host) SetLocation(lat float32, long float32) error {
	return h.Fork().Get("location").Set(location{
		Latitude:  lat,
		Longitude: long,
	}).Commit()
}

func (h *host) Shapes() HostShapesParser {
	return &hostShapes{root: h.root, Query: h.Fork().Get("shapes")}
}

func (s *ssh) Address() (a string) {
	s.Fork().Get("addr").Value(&a)
	return
}

func (s *ssh) Port() uint16 {
	var p int
	if err := s.Fork().Get("port").Value(&p); err != nil {
		p = 22
	}
	return uint16(p)
}

func (s *ssh) SetAddress(addr string) error {
	return s.Fork().Get("addr").Set(addr).Commit()
}

func (s *ssh) SetPort(prt uint16) error {
	return s.Fork().Get("port").Set(int(prt)).Commit()
}

func (s *ssh) SetFullAddress(faddr string) error {
	host, port, err := net.SplitHostPort(faddr)
	if err != nil {
		return err
	}

	prt, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return err
	}

	err = s.SetAddress(host)
	if err != nil {
		return err
	}

	err = s.SetPort(uint16(prt))
	if err != nil {
		return err
	}

	return nil
}

func (s *ssh) Auth() ListParser[string] {
	return &list[string]{root: s.root, Query: s.Fork().Get("auth")}
}

func (hs *hostShapes) List() (l []string) {
	l, _ = hs.Fork().List()
	return
}

func (hs *hostShapes) Instance(s string) InstanceParser {
	return &instShape{root: hs.root, Query: hs.Fork().Get(s)}
}

func (hs *hostShapes) Delete(s string) error {
	return hs.Fork().Get(s).Delete().Commit()
}

func (hs *instShape) Key() (k string) {
	hs.Fork().Get("key").Value(&k)
	return
}

func (hs *instShape) Id() (i string) {
	var err error

	if err = hs.Fork().Get("id").Value(&i); err == nil {
		return
	}

	var k string
	if err := hs.Fork().Get("key").Value(&k); err != nil {
		return
	}

	if i, _, err = generateNodeKeyAndID(k); err == nil {
		hs.Fork().Get("id").Set(i).Commit()
	}

	return
}

// will also set ID
func (hs *instShape) SetKey(key string) error {
	i, _, err := generateNodeKeyAndID(key)
	if err != nil {
		return err
	}

	err = hs.Fork().Get("key").Set(key).Commit()
	hs.Fork().Get("id").Set(i).Commit()

	return err
}

// will also set ID
func (hs *instShape) GenerateKey() error {
	i, key, err := generateNodeKeyAndID("")
	if err != nil {
		return err
	}

	err = hs.Fork().Get("key").Set(key).Commit()
	hs.Fork().Get("id").Set(i).Commit()

	return err
}
