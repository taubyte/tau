package pubsub

import (
	"regexp"

	spec "github.com/taubyte/tau/pkg/specs/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

func (s *Service) getMessagingsMap(matcher *common.MatchDefinition) (*common.MessagingMap, string, string, error) {
	messagingsMap := new(common.MessagingMap)

	globalMessaging, commit, branch, err := s.Tns().Messaging().Global(matcher.Project, spec.DefaultBranches...).List()
	if err != nil {
		return nil, commit, branch, err
	}

	matchMethod := func(m *structureSpec.Messaging) {
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
		matchMethod(m)
	}

	if len(matcher.Application) > 0 {
		relativeMessaging, _, _, err := s.Tns().Messaging().Relative(matcher.Project, matcher.Application, branch).List()
		if err == nil {
			for _, m := range relativeMessaging {
				matchMethod(m)
			}
		}
	}

	return messagingsMap, commit, branch, nil
}
