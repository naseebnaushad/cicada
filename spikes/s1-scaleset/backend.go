package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// jitEnv is the variable the GitHub runner reads its single-use configuration from.
const jitEnv = "ACTIONS_RUNNER_INPUT_JITCONFIG"

var runnerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// dockerBackend runs each runner as a throwaway container through the docker CLI.
// The spike shells out to keep the dependency list short; the real provider will decide
// between the CLI and the API client.
type dockerBackend struct {
	image  string
	log    *slog.Logger
	onExit func(name string)
}

// Start launches the container and returns once docker has been started; a goroutine
// waits for the container to end.
func (d *dockerBackend) Start(_ context.Context, name, jitConfig string) error {
	if !runnerNamePattern.MatchString(name) {
		return fmt.Errorf("invalid runner name %q", name)
	}
	// "-e NAME" with no value makes docker copy the variable from its own environment,
	// so the configuration never appears in a process argument list.
	cmd := exec.Command("docker", "run", "--rm",
		"--name", name,
		"--label", "cicada-spike=1",
		"--user", "runner",
		"-e", jitEnv,
		d.image, "/home/runner/run.sh")
	cmd.Env = append(os.Environ(), jitEnv+"="+jitConfig)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start docker: %w", err)
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			d.log.Info("runner container ended with an error", "runner", name, "error", err)
		}
		d.onExit(name)
	}()
	return nil
}

// Remove force-removes the container. A container that is already gone is not an error.
func (d *dockerBackend) Remove(ctx context.Context, name string) error {
	if !runnerNamePattern.MatchString(name) {
		return fmt.Errorf("invalid runner name %q", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "rm", "--force", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm: %w: %s", err, firstLine(out))
	}
	return nil
}

// checkDocker fails early with a clear message when the daemon is not reachable.
func checkDocker(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		return errors.New("docker is not running or not installed; start it, or use -backend none to only watch job assignment")
	}
	return nil
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}

// noBackend starts nothing. It is for watching which scale set GitHub assigns jobs to
// (spike S2); jobs assigned to it stay queued until the run is cancelled on GitHub.
type noBackend struct{ log *slog.Logger }

func (n noBackend) Start(_ context.Context, name, _ string) error {
	n.log.Info("backend none: not starting a runner", "runner", name)
	return nil
}

func (noBackend) Remove(context.Context, string) error { return nil }
