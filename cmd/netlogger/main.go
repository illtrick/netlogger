package main

import (
	"fmt"
	"os"

	"netlogger/internal/version"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		fmt.Println("netlogger", version.Version)
		return
	}
	fmt.Println("netlogger", version.Version, "- use a subcommand (version)")
}
