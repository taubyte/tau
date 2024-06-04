package structureSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	messagingSpec "github.com/taubyte/tau/pkg/specs/messaging"
)

type Messaging struct {
	Id          string
	Name        string
	Description string
	Tags        []string

	Local     bool
	Match     string
	Regex     bool
	MQTT      bool
	WebSocket bool

	// noset, this is parsed from the tags
	SmartOps []string

	Basic
	Wasm
}

func (m Messaging) GetName() string {
	return m.Name
}

func (m *Messaging) SetId(id string) {
	m.Id = id
}

func (m *Messaging) EmptyPath(branch, commit, project, app string) (*common.TnsPath, error) {
	return messagingSpec.Tns().EmptyPath(branch, commit, project, app)
}

func (m *Messaging) BasicPath(branch, commit, project, app string) (*common.TnsPath, error) {
	return messagingSpec.Tns().BasicPath(branch, commit, project, app, m.Id)
}

func (m *Messaging) IndexValue(branch, project, app string) (*common.TnsPath, error) {
	return messagingSpec.Tns().IndexValue(branch, project, app, m.Id)
}

func (m *Messaging) WebSocketHashPath(project, app string) (*common.TnsPath, error) {
	return messagingSpec.Tns().WebSocketHashPath(project, app)
}

func (m *Messaging) WebSocketPath(hash string) (*common.TnsPath, error) {
	return messagingSpec.Tns().WebSocketPath(hash)
}

func (m *Messaging) GetId() string {
	return m.Id
}
