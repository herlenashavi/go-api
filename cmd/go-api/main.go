// Command go-api scaffolds a new service built on github.com/herlenashavi/go-api.
// See docs/scaffolding.md for the full flag surface and what's still a
// roadmap item (gRPC, mTLS, Tilt/k8s manifests, the custom-metrics
// example, and `go-api add resource` for an existing service).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "new":
		err = runNew(os.Args[2:])
	case "-h", "-help", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "go-api: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-api: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `go-api — scaffold a service built on github.com/herlenashavi/go-api

Usage:
  go-api new <name> -module <path> [flags]

Run "go-api new -h" for the full flag list.
`)
}
