// Package cli implements the barsdig command line.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"barsdig/internal/catalog"
	"barsdig/internal/source"
)

// Version is the barsdig release version. Release builds set it with
// -ldflags "-X barsdig/internal/cli.Version=...".
var Version = "0.1.0-dev"

const usage = `usage: barsdig <command> [arguments]

commands:
  serve  <romfs>   browse, listen to and pick cues in a local web page
  index  <romfs>   scan the RomFS and print a summary and any problems
  export <romfs>   export cues to disk (--out DIR, then --picks FILE, --match TEXT or --bank NAME)
  version          print the version

<romfs> is an extracted RomFS folder (it should contain Sound/Resource and/or Voice/Resource).
`

// Run executes the command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return run(context.Background(), args, stdout, stderr)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "barsdig %s\n", Version)
		return 0
	case "index":
		return cmdIndex(args[1:], stdout, stderr)
	case "export":
		return cmdExport(args[1:], stdout, stderr)
	case "serve":
		return cmdServe(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "barsdig: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// parseFlags parses flags that may appear before or after positional arguments.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func defaultCacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "barsdig")
}

// openCatalog opens the RomFS and loads (or builds) its catalog.
func openCatalog(romfs, cacheDir string) (source.Source, *catalog.Catalog, bool, error) {
	src, err := source.NewDir(romfs)
	if err != nil {
		return nil, nil, false, err
	}
	cat, cached, err := catalog.Load(src, catalog.Options{}, cacheDir)
	if err != nil {
		return nil, nil, false, err
	}
	return src, cat, cached, nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("barsdig "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func oneRomFS(cmd string, pos []string, stderr io.Writer) (string, bool) {
	if len(pos) != 1 {
		fmt.Fprintf(stderr, "usage: barsdig %s <romfs> [flags]\n", cmd)
		return "", false
	}
	return pos[0], true
}
