// Package cli adapts command-line arguments to varco operations.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/HarnageaGabriel/varco/internal/version"
)

const usage = `Usage: varco <command> [options]

Commands:
  version    print build version
  help       show this help
  diff       compare endpoints (not implemented)
  plan       create a probe plan (not implemented)
  run        execute a probe plan (not implemented)
`

// Run executes args and returns a documented process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			_, _ = fmt.Fprintln(stderr, "help: unexpected arguments")
			return 2
		}
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "diff", "plan", "run":
		_, _ = fmt.Fprintf(stderr, "%s: not implemented\n", args[0])
		return 2
	default:
		_, _ = fmt.Fprintf(stderr, "unknown subcommand %q\n", args[0])
		return 2
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print version as JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "version: unexpected arguments")
		return 2
	}

	info := version.Current()
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(info); err != nil {
			_, _ = fmt.Fprintln(stderr, "version:", err)
			return 4
		}
		return 0
	}

	_, _ = fmt.Fprintf(stdout, "varco %s (commit %s, built %s)\n", info.Version, info.Commit, info.Date)
	return 0
}
