package main

import (
	"fmt"
	"os"
)

func main() {
	cmd, err := newRootCommand()
	if err == nil {
		err = cmd.Execute()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
