package functionLib

import (
	"github.com/taubyte/tau/pkg/schema/functions"
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/common"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/utils/id"
)

type getter struct {
	project     project.Project
	application string
	function    functions.Function
}

func get(name string) (info getter, err error) {
	info.project, info.application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	info.function, err = info.project.Function(name, info.application)
	if err != nil {
		return
	}

	return
}

func list() (project project.Project, application string, functions []string, err error) {
	project, application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	local, global := project.Get().Functions(application)
	if len(application) > 0 {
		functions = local
	} else {
		functions = global
	}

	return
}

func set(function *structureSpec.Function, new bool) (info getter, err error) {
	info, err = get(function.Name)
	if err != nil {
		return
	}

	if new {
		function.Id = id.Generate(info.project.Get().Id(), function.Name)
	} else if function.Type != info.function.Get().Type() {
		err = info.function.Delete("trigger", "domains")
		if err != nil {
			return
		}

		switch function.Type {
		case common.FunctionTypeHttp, common.FunctionTypeHttps:
			function.Protocol = ""
			function.Command = ""
			function.Channel = ""
			function.Local = false
		case common.FunctionTypeP2P, common.FunctionTypePubSub:
			function.Method = ""
			function.Domains = nil
			function.Paths = nil
			switch function.Type {
			case common.FunctionTypeP2P:
				function.Channel = ""
			case common.FunctionTypePubSub:
				function.Command = ""
				function.Protocol = ""
			}
		}
	}

	err = info.function.SetWithStruct(true, function)
	if err != nil {
		return
	}

	return
}
