package status

import (
	"fmt"
	"os"
	"strconv"
	"time"

	patrickClient "github.com/taubyte/tau/tools/tau/clients/patrick_client"
	"github.com/taubyte/tau/tools/tau/lib/follow"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

var Command = &cli.Command{
	Name:  "status",
	Usage: "last build of each repository and what each node serves, for the selected project",
	Flags: []cli.Flag{
		&cli.BoolFlag{Name: "watch", Aliases: []string{"w"}, Usage: "refresh every 2 seconds"},
	},
	Action: run,
}

func run(ctx *cli.Context) error {
	st, err := tcc.Open()
	if err != nil {
		return err
	}
	project, err := st.ProjectID()
	if err != nil {
		return err
	}
	p, err := patrickClient.Load()
	if err != nil {
		return err
	}
	// read once: the project's config does not change while we watch
	names := map[int]string{}
	for _, group := range []string{"websites", "libraries"} {
		list, _ := st.List(group)
		for _, n := range list {
			doc, err := st.Doc(group, n)
			if err != nil {
				continue
			}
			id, _ := tcc.Get(doc, []string{"source", "github", "id"}).(string)
			if i, err := strconv.Atoi(id); err == nil {
				names[i] = group[:len(group)-1] + " " + n
			}
		}
	}
	targets, err := follow.Targets(st, true)
	if err != nil {
		return err
	}

	for {
		rep, err := follow.GetStatus(p, project, names, targets)
		if err != nil {
			return err
		}
		if !output.Render(rep) {
			if ctx.Bool("watch") {
				fmt.Print("\033[H\033[2J")
			}
			follow.WriteStatus(os.Stdout, rep)
		}
		if !ctx.Bool("watch") {
			return nil
		}
		select {
		case <-ctx.Context.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}
