// Command barsdig browses, auditions and exports audio from BARS/BWAV files.
package main

import (
	"os"

	"barsdig/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
