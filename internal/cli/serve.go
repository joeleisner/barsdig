package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"barsdig/internal/server"
)

func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("serve", stderr)
	cacheDir := fs.String("cache-dir", defaultCacheDir(), "catalog cache directory (empty disables caching)")
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on")
	anyHost := fs.Bool("allow-nonloopback", false, "allow listening on a non-loopback address (exposes your audio to the network)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	out := fs.String("out", "./barsdig-export", "directory the Export button writes to")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return 2
	}
	romfs, ok := oneRomFS("serve", pos, stderr)
	if !ok {
		return 2
	}
	if !*anyHost && !loopbackAddr(*addr) {
		fmt.Fprintf(stderr, "barsdig serve: %q is a non-loopback address; use 127.0.0.1 or pass --allow-nonloopback\n", *addr)
		return 2
	}
	outAbs, err := filepath.Abs(*out)
	if err != nil {
		fmt.Fprintf(stderr, "barsdig: %v\n", err)
		return 1
	}
	src, cat, cached, err := openCatalog(romfs, *cacheDir)
	if err != nil {
		fmt.Fprintf(stderr, "barsdig: %v\n", err)
		return 1
	}
	// Only serve traps interrupts (for a graceful shutdown); index and export keep the default Ctrl+C behaviour.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "barsdig: %v\n", err)
		return 1
	}
	url := "http://" + ln.Addr().String() + "/"
	note := ""
	if cached {
		note = " (cached)"
	}
	fmt.Fprintf(stdout, "barsdig: %d cues%s, %d problems (run 'barsdig index' to list them)\n", len(cat.Cues), note, len(cat.Problems))
	fmt.Fprintf(stdout, "barsdig: open %s\n", url)
	fmt.Fprintf(stdout, "barsdig: exports go to %s\n", outAbs)
	fmt.Fprintln(stdout, "barsdig: press Ctrl+C to stop")

	srv := &http.Server{
		Handler:           server.New(cat, src, server.Options{OutDir: outAbs, ToolVersion: Version, AnyHost: *anyHost}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	if !*noOpen {
		openBrowser(url)
	}
	select {
	case err := <-errc:
		fmt.Fprintf(stderr, "barsdig: %v\n", err)
		return 1
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		srv.Close()
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "barsdig: %v\n", err)
		return 1
	}
	return 0
}
