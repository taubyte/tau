package messaging_test

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/messaging"
	"github.com/taubyte/tau/pkg/schema/project"
)

func ExampleMessaging() {
	// Create a new file system in memory
	fs := afero.NewMemMapFs()

	// Open a new project with a virtual file system
	project, err := project.Open(project.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	// Create or open an messaging
	msg, err := project.Messaging("test_msg", "")
	if err != nil {
		return
	}

	// Set and write messaging fields
	err = msg.Set(true,
		messaging.Id("QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc"),
		messaging.Description("a basic messaging"),
		messaging.Tags([]string{"tag1", "tag2"}),
		messaging.Local(false),
		messaging.Channel(false, "simpleChannel"),
		messaging.Bridges(false, true),
	)
	if err != nil {
		return
	}

	// Display the Description
	fmt.Println(msg.Get().Description())

	// Open the config.yaml of the messaging
	config, err := afero.ReadFile(fs, "/messaging/test_msg.yaml")
	if err != nil {
		return
	}

	// Print config.yaml
	fmt.Println(string(config))

	// Output: a basic messaging
	// id: QmaEBKzOyrYL1D6gtqD86Nyr2hvXAxWHcMCu9ffxLaByMc
	// description: a basic messaging
	// tags:
	//     - tag1
	//     - tag2
	// local: false
	// channel:
	//     regex: false
	//     match: simpleChannel
	// bridges:
	//     mqtt:
	//         enable: false
	//     websocket:
	//         enable: true
}
