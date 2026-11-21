// Command s3-manifest is spike S3 from documents/Action Plan.md.
//
// Question: can a CLI create a least-privilege GitHub App in one browser click, using the
// App manifest flow with a loopback callback?
//
// It starts a server on 127.0.0.1, sends the user to GitHub with a manifest, receives the
// temporary code on the callback, exchanges it for the App's credentials and stores them
// outside the repository with owner-only permissions. The private key is never printed.
//
// This is spike code. The parts that survive move to the onboarding use case in
// Milestone 1.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "s3-manifest:", err)
		os.Exit(1)
	}
}

func run() error {
	defaultOut := "cicada-spike"
	if dir, err := os.UserConfigDir(); err == nil {
		defaultOut = filepath.Join(dir, "cicada-spike")
	}

	var cfg config
	var noBrowser bool
	var timeout time.Duration
	flag.StringVar(&cfg.Org, "org", "", "organisation that will own the App (empty: your personal account)")
	flag.StringVar(&cfg.Scope, "scope", "", "runner scope the App is for: org or repo (default: org with -org, else repo)")
	flag.StringVar(&cfg.Name, "name", "", "App name, unique on GitHub, at most 34 characters (default: generated)")
	flag.StringVar(&cfg.GitHubURL, "github", "https://github.com", "GitHub web URL")
	flag.StringVar(&cfg.APIURL, "api", "https://api.github.com", "GitHub API URL")
	flag.StringVar(&cfg.OutDir, "out", defaultOut, "folder for the App credentials (must be outside any repository)")
	flag.BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	flag.DurationVar(&timeout, "timeout", 15*time.Minute, "how long to wait for the browser steps")
	flag.Parse()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	defer ln.Close()

	f, err := newFlow(cfg, ln.Addr().String())
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           f.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      45 * time.Second, // the callback waits on one GitHub API call
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	start := "http://" + ln.Addr().String() + "/"
	fmt.Println("Open this page and press the button to create the GitHub App:")
	fmt.Println("  " + start)
	if !noBrowser {
		openBrowser(start)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case out := <-f.done:
		if out.err != nil {
			return out.err
		}
		app := out.app
		fmt.Println()
		fmt.Println("App created.")
		fmt.Printf("  name:       %s\n", app.Name)
		fmt.Printf("  id:         %d\n", app.ID)
		fmt.Printf("  owner:      %s\n", app.Owner.Login)
		fmt.Printf("  saved in:   %s (owner-only; the private key was not printed)\n", f.cfg.OutDir)
		fmt.Printf("  install it: %s/installations/new\n", app.HTMLURL)
		fmt.Printf("  delete it:  %s (App settings, Advanced)\n", f.settingsURL(app))
		return nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s waiting for the browser steps", timeout)
		}
		return errors.New("cancelled")
	}
}

// openBrowser tries to open url in the default browser. Failure is fine: the URL is
// already printed.
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
	_ = cmd.Start()
}
