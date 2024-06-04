package pubsub

import (
	"regexp"

	spec "github.com/taubyte/tau/pkg/specs/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

func (s *Service) GetMessagingsMap(matcher *common.MatchDefinition) (messagingsMap *common.MessagingMap, err error) {
	messagingsMap = new(common.MessagingMap)

	globalMessaging, err := s.Tns().Messaging().Global(matcher.Project, spec.DefaultBranch).List()
	if err != nil {
		return nil, err
	}

	matchMethod := func(m *structureSpec.Messaging, application string) {
		var foundMatch bool
		if m.Match == matcher.Channel {
			foundMatch = true
		} else if m.Regex {
			regMatch, _ := regexp.Match(m.Match, []byte(matcher.Channel))
			if regMatch {
				foundMatch = true
			}
		}

		if foundMatch {
			messagingsMap.HasAny = true
			if m.WebSocket {
				messagingsMap.WebSocket.Push(matcher.Project, "", m)
			}
			messagingsMap.Function.Push(matcher.Project, "", m)
		}
	}

	for _, m := range globalMessaging {
		matchMethod(m, "")
	}

	if len(matcher.Application) > 0 {
		relativeMessaging, err := s.Tns().Messaging().Relative(matcher.Project, matcher.Application, spec.DefaultBranch).List()
		if err != nil {
			return nil, err
		}

		for _, m := range relativeMessaging {
			matchMethod(m, matcher.Application)
		}
	}

	return
}
