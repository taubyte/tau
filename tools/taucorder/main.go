package main

import (
	"fmt"
	"os"

	"github.com/common-nighthawk/go-figure"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/tools/taucorder/common"
	"github.com/taubyte/tau/tools/taucorder/prompt"
	"github.com/urfave/cli/v2"
)

var (
	node    peer.Node
	scanner prompt.ScannerHandler
)

var app = &cli.App{
	UseShortOptionHandling: true,
	EnableBashCompletion:   true,
	Action:                 func(ctx *cli.Context) error { return nil },
	Commands: []*cli.Command{
		dreamCmd,
		prodCmd,
	},
}

func main() {
	err := app.RunContext(common.GlobalContext, os.Args)
	if err != nil {
		fmt.Println("Parsing command line failed with error:", err)
		return
	}

	if node == nil {
		fmt.Println()
		fmt.Println("You need to select a cloud")
		return
	}

	banner()

	p, err := prompt.New(common.GlobalContext)
	if err != nil {
		fmt.Println("Prompt new failed with error:", err)
		return
	}

	err = p.Run(prompt.Node(node), prompt.Scanner(scanner))
	if err != nil {
		fmt.Println("Running prompt failed with error:", err)
		return
	}
}

func banner() {
	myFigure := figure.NewColorFigure(
		"TAUCORDER",
		"speed",
		"green",
		true,
	)
	myFigure.Print()
	fmt.Println()
}
