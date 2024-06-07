package main

import (
	"fmt"

	"github.com/common-nighthawk/go-figure"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/tools/taucorder/common"
	"github.com/taubyte/tau/tools/taucorder/prompt"
)

var node peer.Node

func main() {
	err := ParseCommandLine()
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

	err = p.Run(node)
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
