package common

import (
	"regexp"

	structureSpec "github.com/taubyte/tau/legacy/specs/structure"
)

type MessagingItem struct {
	project     string
	application string
	config      *structureSpec.Messaging

	// re is config.Match compiled, once, when the item is built.
	//
	// IT USED TO BE regexp.Match PER CALL, which compiles the pattern every time
	// -- and Matches is asked on the serviceable lookup path, so a channel with a
	// regex matcher paid a full compile for every message. Measured on the
	// shapes here: 2867ns to compile-and-match against 129ns to match something
	// already compiled.
	//
	// nil when the pattern does not compile, which is the same answer the
	// one-shot form gave: it returned an error nobody read, and a pattern that
	// cannot compile matches nothing.
	re *regexp.Regexp
}

func (i *MessagingItem) Project() string {
	return i.project
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

	item := &MessagingItem{
		project:     project,
		application: application,
		config:      m,
	}
	if m.Regex {
		// Error dropped on purpose -- see MessagingItem.re.
		item.re, _ = regexp.Compile(m.Match)
	}

	mmi.Items = append(mmi.Items, item)
}

func (mmi *MessagingMapItem) Matches(channel string) []*structureSpec.Messaging {
	ret := make([]*structureSpec.Messaging, 0)
	for _, m := range mmi.Items {
		if m.config.Match == channel {
			ret = append(ret, m.config)
			continue
		}
		if m.re != nil && m.re.MatchString(channel) {
			ret = append(ret, m.config)
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
