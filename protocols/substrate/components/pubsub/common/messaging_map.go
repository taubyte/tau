package common

import (
	"regexp"

	"github.com/ipfs/go-cid"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var _ iface.Messaging = &MessagingItem{}

type MessagingItem struct {
	project     string
	application string
	config      *structureSpec.Messaging
}

func (i *MessagingItem) Project() (cid.Cid, error) {
	return cid.Decode(i.project)
}

func (i *MessagingItem) Application() string {
	return i.application
}

func (i *MessagingItem) Config() *structureSpec.Messaging {
	return i.config
}

type MessagingMapItem struct {
	Items []*MessagingItem
}

type MessagingMap struct {
	Function  MessagingMapItem
	WebSocket MessagingMapItem
	HasAny    bool
}

func (mmi *MessagingMapItem) Len() int {
	return len(mmi.Items)
}

func (mmi *MessagingMapItem) Push(project, application string, m *structureSpec.Messaging) {
	if len(mmi.Items) == 0 {
		mmi.Items = make([]*MessagingItem, 0)
	}

	mmi.Items = append(mmi.Items, &MessagingItem{
		project:     project,
		application: application,
		config:      m,
	})
}

func (mmi *MessagingMapItem) Matches(channel string) []*structureSpec.Messaging {
	ret := make([]*structureSpec.Messaging, 0)
	for _, m := range mmi.Items {
		if m.config.Match == channel {
			ret = append(ret, m.config)
			continue
		}
		if m.config.Regex {
			match, _ := regexp.Match(m.config.Match, []byte(channel))
			if match {
				ret = append(ret, m.config)
			}
		}
	}

	return ret
}

// Used for getting names of messaging channels in tests
func (mmi *MessagingMapItem) Names() []string {
	names := make([]string, len(mmi.Items))
	for idx, m := range mmi.Items {
		names[idx] = m.config.Name
	}

	return names
}
