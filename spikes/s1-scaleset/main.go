// Command s1-scaleset is spikes S1 and S2 from documents/Action Plan.md.
//
// S1: can the GitHub App made by spike S3 register a runner scale set on a repository of
// a personal account, receive a job and run it on a single-use runner?
//
// S2: when two scale sets share a label, how does GitHub spread jobs between them? Run
// two copies with different -name values and the same -labels, queue several jobs, and
// compare the logs.
//
// The program registers (or reuses) a scale set, long-polls for jobs, starts one
// throwaway Docker container per assigned job, and deletes the scale set on exit.
//
// This is spike code. What survives moves into the forge adapter and the scaler use case
// in Milestone 1.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

// defaultRunnerGroupID is the ID GitHub gives the "default" runner group.
const defaultRunnerGroupID = 1

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

type options struct {
	url        string
	name       string
	labels     string
	maxRunners int
	backend    string
	image      string
	credsDir   string
	keep       bool
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	host, _ := os.Hostname()
	host = sanitizeName(host)
	defaultCreds := "cicada-spike"
	if dir, err := os.UserConfigDir(); err == nil {
		defaultCreds = filepath.Join(dir, "cicada-spike")
	}

	var o options
	flag.StringVar(&o.url, "url", "", "where to register: https://github.com/OWNER/REPO or https://github.com/ORG (required)")
	flag.StringVar(&o.name, "name", "cicada-spike-"+host, "scale set name, unique per runner group")
	flag.StringVar(&o.labels, "labels", "cicada-spike", "comma-separated labels workflows target with runs-on")
	flag.IntVar(&o.maxRunners, "max", 2, "most runners at once")
	flag.StringVar(&o.backend, "backend", "docker", "docker: run jobs in containers; none: only watch job assignment")
	flag.StringVar(&o.image, "image", "ghcr.io/actions/actions-runner:latest", "runner image for the docker backend")
	flag.StringVar(&o.credsDir, "creds", defaultCreds, "folder with the App credentials from spike S3")
	flag.BoolVar(&o.keep, "keep", false, "leave the scale set registered on exit")
	flag.Parse()

	t, err := parseTarget(o.url)
	if err != nil {
		return err
	}
	labels, err := parseLabels(o.labels)
	if err != nil {
		return err
	}
	if !labelPattern.MatchString(o.name) {
		return fmt.Errorf("scale set name %q must be letters, digits, dots, dashes or underscores", o.name)
	}
	if o.maxRunners < 1 || o.maxRunners > 16 {
		return errors.New("-max must be between 1 and 16")
	}

	if o.backend != "docker" && o.backend != "none" {
		return fmt.Errorf("-backend %q must be docker or none", o.backend)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Check the backend before anything is created on GitHub.
	if o.backend == "docker" {
		if err := checkDocker(ctx); err != nil {
			return err
		}
	}

	creds, err := loadAppCreds(o.credsDir)
	if err != nil {
		return err
	}
	appToken, err := appJWT(creds, time.Now())
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	installationID, err := findInstallation(ctx, httpClient, apiURLFor(t.WebURL), t, appToken)
	if errors.Is(err, errNotInstalled) {
		return fmt.Errorf("the App is not installed on %s; install it at %s/apps/%s/installations/new", t, t.WebURL, creds.Slug)
	}
	if err != nil {
		return err
	}
	log.Info("app installation found", "target", t.String(), "installation", installationID)

	info := scaleset.SystemInfo{System: "cicada", Subsystem: "spike-s1", Version: "0.0.0-dev", CommitSHA: "NA"}
	client, err := scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
		GitHubConfigURL: t.ConfigURL(),
		GitHubAppAuth: scaleset.GitHubAppAuth{
			ClientID:       creds.ClientID,
			InstallationID: installationID,
			PrivateKey:     creds.PrivateKey,
		},
		SystemInfo: info,
	})
	if err != nil {
		return fmt.Errorf("create scale set client: %w", err)
	}

	set, err := client.GetRunnerScaleSet(ctx, defaultRunnerGroupID, o.name)
	if err != nil {
		return fmt.Errorf("look up scale set: %w", err)
	}
	if set == nil {
		set, err = client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
			Name:          o.name,
			RunnerGroupID: defaultRunnerGroupID,
			Labels:        labels,
			RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
		})
		if err != nil {
			return fmt.Errorf("create scale set: %w", err)
		}
		log.Info("scale set created", "name", set.Name, "id", set.ID, "labels", labelNames(set.Labels))
	} else {
		log.Info("scale set already existed, reusing it", "name", set.Name, "id", set.ID, "labels", labelNames(set.Labels))
	}
	info.ScaleSetID = set.ID
	client.SetSystemInfo(info)

	// Clean-up must still run after Ctrl-C, so it uses a context that is not cancelled.
	cleanupCtx := context.WithoutCancel(ctx)
	if !o.keep {
		defer func() {
			c, cancel := context.WithTimeout(cleanupCtx, 30*time.Second)
			defer cancel()
			if err := client.DeleteRunnerScaleSet(c, set.ID); err != nil {
				log.Warn("delete scale set (remove it by hand if it remains)", "id", set.ID, "error", err)
				return
			}
			log.Info("scale set deleted", "id", set.ID)
		}()
	}

	s := &scaler{
		log:        log,
		jit:        client,
		scaleSetID: set.ID,
		maxRunners: o.maxRunners,
		newName:    func() string { return "cicada-spike-" + randomSuffix() },
		runners:    make(map[string]struct{}),
	}
	if o.backend == "docker" {
		s.backend = &dockerBackend{image: o.image, log: log, onExit: s.runnerExited}
	} else {
		s.backend = noBackend{log: log}
	}
	defer func() {
		c, cancel := context.WithTimeout(cleanupCtx, 60*time.Second)
		defer cancel()
		s.shutdown(c)
	}()

	session, err := client.MessageSessionClient(ctx, set.ID, host)
	if err != nil {
		return fmt.Errorf("open message session (is another copy using scale set %q?): %w", o.name, err)
	}
	defer func() {
		c, cancel := context.WithTimeout(cleanupCtx, 15*time.Second)
		defer cancel()
		_ = session.Close(c)
	}()
	s.jobs = session

	l, err := listener.New(session, listener.Config{ScaleSetID: set.ID, MaxRunners: o.maxRunners})
	if err != nil {
		return fmt.Errorf("create listener: %w", err)
	}
	log.Info("listening for jobs; press Ctrl-C to stop", "runs-on", labelNames(labels), "max", o.maxRunners, "backend", o.backend)
	if err := l.Run(ctx, s); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("listener: %w", err)
	}
	log.Info("stopping")
	return nil
}

func parseLabels(raw string) ([]scaleset.Label, error) {
	var labels []scaleset.Label
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if !labelPattern.MatchString(name) {
			return nil, fmt.Errorf("label %q must be letters, digits, dots, dashes or underscores", name)
		}
		labels = append(labels, scaleset.Label{Name: name})
	}
	if len(labels) == 0 {
		return nil, errors.New("at least one label is required")
	}
	return labels, nil
}

func labelNames(labels []scaleset.Label) []string {
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}
	return names
}

// sanitizeName turns a hostname into something safe for a scale set name.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '.' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 30 {
		out = strings.Trim(out[:30], "-")
	}
	if out == "" {
		return "host"
	}
	return out
}

func randomSuffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on supported platforms; a clash only delays a job.
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b)
}
