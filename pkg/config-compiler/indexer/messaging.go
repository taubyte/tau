package indexer

import (
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	messagingSpec "github.com/taubyte/tau/pkg/specs/messaging"
	"github.com/taubyte/utils/maps"
)

func Messaging(ctx *IndexContext, project projectSchema.Project, urlIndex map[string]interface{}) error {
	if urlIndex == nil {
		return fmt.Errorf("urlIndex received is nil")
	}

	if ctx.Obj == nil {
		return fmt.Errorf("obj received is nil")
	}

	if ctx.Commit == "" || ctx.Branch == "" || ctx.ProjectId == "" {
		return fmt.Errorf("commit, branch, and project required for IndexContext: `%v`", ctx)
	}

	msgObj, ok := ctx.Obj[string(messagingSpec.PathVariable)]
	if !ok {
		return nil // This shouldn't be breaking,  it just means there are no msgs
	}

	for _, message := range maps.SafeInterfaceToStringKeys(msgObj) {
		name, err := maps.String(maps.SafeInterfaceToStringKeys(message), "name")
		if err != nil {
			return err
		}

		msg, err := project.Messaging(name, ctx.AppName)
		if err != nil {
			return err
		}

		if len(msg.Get().Id()) == 0 {
			return fmt.Errorf("Messaging `%s` not found", msg.Get().Name())
		}

		if !msg.Get().WebSocket() {
			continue
		}

		wsPath, err := messagingSpec.Tns().WebSocketHashPath(ctx.ProjectId, ctx.AppId)
		if err != nil {
			return err
		}

		tnsPath, err := messagingSpec.Tns().IndexValue(
			ctx.Branch,
			ctx.ProjectId,
			ctx.AppId,
			msg.Get().Id(),
		)
		if err != nil {
			return err
		}

		// create entry if empty
		if _, exists := urlIndex[wsPath.String()]; !exists {
			urlIndex[wsPath.String()] = make([]string, 0)
		}

		// check if value not there already
		skip := false
		for _, val := range urlIndex[wsPath.String()].([]string) {
			if tnsPath.String() == val {
				skip = true
				break
			}
		}

		// add value (path to object) to the list
		if !skip {
			urlIndex[wsPath.String()] = append(urlIndex[wsPath.String()].([]string), tnsPath.String())
		}
	}

	return nil
}
