// Command varco compares HTTP endpoints.
package main

import (
	"os"

	"github.com/HarnageaGabriel/varco/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
