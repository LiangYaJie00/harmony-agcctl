package main

import (
	"os"

	"github.com/LiangYaJie00/harmony-agcctl/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Dependencies{}))
}
