package smartOps

import (
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/smartops"
	"github.com/taubyte/odo/protocols/node/components/smartops/instance"
)

func (s *Service) Run(caller iface.SmartOpEventCaller, smartOpIds []string) (uint32, error) {
	if len(smartOpIds) < 1 {
		return 0, nil
	}

	projectCid, err := caller.Project()
	if err != nil {
		return 0, fmt.Errorf("getting project cid from smartOp caller failed with: %s", err)
	}

	project := projectCid.String()
	client := s.Tns().SmartOp().All(project, caller.Application(), s.Branch())

	commit, err := client.Commit(project, s.Branch())
	if err != nil {
		return 0, fmt.Errorf("getting client commit from (%s/%s) failed with: %s", project, s.Branch(), err)
	}

	for _, smartOpId := range smartOpIds {
		_instance, ok := s.cache.Get(project, caller.Application(), smartOpId, caller.Context())
		if !ok {
			config, err := client.GetByIdCommit(smartOpId, commit)
			if err != nil {
				return 0, fmt.Errorf("getting config from commit failed with: %s", err)
			}

			_instance, err = instance.Initialize(s, instance.InstanceContext{
				Config:      *config,
				Project:     project,
				Application: caller.Application(),
				Commit:      commit,
			})
			if err != nil {
				return 0, fmt.Errorf("initializing instance of SmartOp(%s/%s/%s) failed with: %s", project, caller.Application(), smartOpId, err)
			}

			err = s.cache.Put(project, caller.Application(), smartOpId, caller.Context(), _instance)
			if err != nil {
				return 0, fmt.Errorf("caching instance of SmartOp(%s/%s/%s) failed with: %s", project, caller.Application(), smartOpId, err)
			}
		}

		returnVal, err := _instance.Run(caller)
		if err != nil {
			return 0, fmt.Errorf("running instance of SmartOp(%s/%s/%s) failed with: %s", project, caller.Application(), smartOpId, err)
		}

		if returnVal != 0 {
			return returnVal, nil
		}
	}

	return 0, nil
}
