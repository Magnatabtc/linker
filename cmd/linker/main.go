package main

import (
	"context"
	"fmt"
	"os"

	"linker/internal/app"
)

func main() {
	exitCode, err := app.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
