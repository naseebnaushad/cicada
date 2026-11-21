package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// projectURL is the homepage shown on the App's public page.
const projectURL = "https://github.com/naseebnaushad/cicada"

// maxAPIResponse caps how much of a GitHub API response is read.
const maxAPIResponse = 1 << 20

var (
	orgPattern  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,33}$`)
	codePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
)

// config is what the user chose on the command line.
type config struct {
	Org       string // owning organisation; empty means the signed-in personal account
	Scope     string // "org" or "repo": which runner level the App must manage
	Name      string // App name
	GitHubURL string // web base URL, no trailing slash
	APIURL    string // API base URL, no trailing slash
	OutDir    string // where credentials are stored
}

// manifest is the JSON document GitHub turns into an App registration.
// Webhooks are left out on purpose: Cicada only makes outbound calls.
type manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	RedirectURL        string            `json:"redirect_url"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

// appCredentials is the part of the conversion response this spike uses.
type appCredentials struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	ClientID string `json:"client_id"`
	HTMLURL  string `json:"html_url"`
	PEM      string `json:"pem"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// outcome is the single result of the flow.
type outcome struct {
	app appCredentials
	err error
}

// flow holds the state of one manifest registration.
type flow struct {
	cfg    config
	addr   string // loopback host:port the server listens on
	state  string // CSRF token GitHub echoes back
	client *http.Client

	once sync.Once
	done chan outcome
}

// newFlow validates cfg, fills defaults and prepares a flow bound to addr.
func newFlow(cfg config, addr string) (*flow, error) {
	cfg.GitHubURL = strings.TrimRight(cfg.GitHubURL, "/")
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	for _, raw := range []string{cfg.GitHubURL, cfg.APIURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "https" && !isLoopbackHTTP(u)) {
			return nil, fmt.Errorf("%q must be an https URL", raw)
		}
	}
	if cfg.Org != "" && !orgPattern.MatchString(cfg.Org) {
		return nil, fmt.Errorf("%q is not a valid organisation name", cfg.Org)
	}
	if cfg.Scope == "" {
		cfg.Scope = "repo"
		if cfg.Org != "" {
			cfg.Scope = "org"
		}
	}
	if _, err := permissionsFor(cfg.Scope); err != nil {
		return nil, err
	}
	state, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	if cfg.Name == "" {
		cfg.Name = "cicada-" + state[:8]
	}
	if !namePattern.MatchString(cfg.Name) {
		return nil, fmt.Errorf("app name %q must be 1-34 letters, digits, spaces, dots, dashes or underscores", cfg.Name)
	}
	if cfg.OutDir == "" {
		return nil, errors.New("output folder is empty")
	}
	return &flow{
		cfg:    cfg,
		addr:   addr,
		state:  state,
		client: &http.Client{Timeout: 30 * time.Second},
		done:   make(chan outcome, 1),
	}, nil
}

// isLoopbackHTTP allows plain http only for a local test server.
func isLoopbackHTTP(u *url.URL) bool {
	return u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")
}

// permissionsFor returns the smallest permission set for a runner scope.
//
//	org:  manage organisation-level self-hosted runners
//	repo: manage repository-level self-hosted runners (GitHub puts this under Administration)
func permissionsFor(scope string) (map[string]string, error) {
	switch scope {
	case "org":
		return map[string]string{"organization_self_hosted_runners": "write", "metadata": "read"}, nil
	case "repo":
		return map[string]string{"administration": "write", "metadata": "read"}, nil
	default:
		return nil, fmt.Errorf("scope %q must be org or repo", scope)
	}
}

func (f *flow) manifest() manifest {
	perms, _ := permissionsFor(f.cfg.Scope) // validated in newFlow
	return manifest{
		Name:               f.cfg.Name,
		URL:                projectURL,
		RedirectURL:        "http://" + f.addr + "/callback",
		Public:             false,
		DefaultPermissions: perms,
		DefaultEvents:      []string{},
	}
}

// registerURL is where the browser posts the manifest.
func (f *flow) registerURL() string {
	path := "/settings/apps/new"
	if f.cfg.Org != "" {
		path = "/organizations/" + url.PathEscape(f.cfg.Org) + "/settings/apps/new"
	}
	return f.cfg.GitHubURL + path + "?state=" + url.QueryEscape(f.state)
}

// settingsURL is the App's settings page, where it can be deleted.
func (f *flow) settingsURL(app appCredentials) string {
	if f.cfg.Org != "" {
		return f.cfg.GitHubURL + "/organizations/" + url.PathEscape(f.cfg.Org) + "/settings/apps/" + url.PathEscape(app.Slug)
	}
	return f.cfg.GitHubURL + "/settings/apps/" + url.PathEscape(app.Slug)
}

func (f *flow) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", f.handleStart)
	mux.HandleFunc("GET /callback", f.handleCallback)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A page on another site can make the browser call 127.0.0.1 under a different
		// name (DNS rebinding); only answer requests addressed to our exact address.
		if r.Host != f.addr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action "+f.cfg.GitHubURL)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

var startPage = template.Must(template.New("start").Parse(`<!doctype html>
<meta charset="utf-8"><title>Cicada: create GitHub App</title>
<body style="font-family:system-ui;max-width:36rem;margin:4rem auto;padding:0 1rem">
<h1>Create the Cicada GitHub App</h1>
<p>GitHub will show the App name <b>{{.Name}}</b> and these permissions before anything is created:</p>
<ul>{{range $k, $v := .Permissions}}<li>{{$k}}: {{$v}}</li>{{end}}</ul>
<form action="{{.Action}}" method="post">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<button type="submit" style="font-size:1.1rem;padding:.5rem 1rem">Continue to GitHub</button>
</form>
</body>`))

var resultPage = template.Must(template.New("result").Parse(`<!doctype html>
<meta charset="utf-8"><title>Cicada</title>
<body style="font-family:system-ui;max-width:36rem;margin:4rem auto;padding:0 1rem">
<h1>{{.Title}}</h1><p>{{.Text}}</p>
{{if .Link}}<p><a href="{{.Link}}" rel="noreferrer">Install the App on your account or organisation</a></p>{{end}}
</body>`))

func (f *flow) handleStart(w http.ResponseWriter, _ *http.Request) {
	m := f.manifest()
	raw, err := json.Marshal(m)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = startPage.Execute(w, map[string]any{
		"Name":        m.Name,
		"Permissions": m.DefaultPermissions,
		"Action":      f.registerURL(),
		"Manifest":    string(raw),
	})
}

func (f *flow) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.state)) != 1 {
		f.page(w, http.StatusBadRequest, "Request rejected", "The state value did not match. Start again from the terminal.", "")
		return
	}
	code := q.Get("code")
	if !codePattern.MatchString(code) {
		f.page(w, http.StatusBadRequest, "Request rejected", "The code from GitHub is missing or malformed.", "")
		return
	}

	// The code works once, so only the first valid callback is acted on.
	first := false
	f.once.Do(func() { first = true })
	if !first {
		f.page(w, http.StatusConflict, "Already handled", "This sign-up was already completed. Return to the terminal.", "")
		return
	}

	app, err := f.exchange(r.Context(), code)
	if err == nil {
		err = saveCredentials(f.cfg.OutDir, app)
	}
	if err != nil {
		// Details go to the terminal; the browser gets a generic message.
		f.page(w, http.StatusBadGateway, "Something went wrong", "The App could not be set up. See the terminal for details.", "")
		f.done <- outcome{err: err}
		return
	}
	f.page(w, http.StatusOK, "App created", "The credentials are stored on this machine. You can close this tab after installing the App.", app.HTMLURL+"/installations/new")
	f.done <- outcome{app: app}
}

func (f *flow) page(w http.ResponseWriter, status int, title, text, link string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = resultPage.Execute(w, map[string]string{"Title": title, "Text": text, "Link": link})
}

// exchange trades the temporary code for the App's credentials.
func (f *flow) exchange(ctx context.Context, code string) (appCredentials, error) {
	var app appCredentials
	endpoint := f.cfg.APIURL + "/app-manifests/" + url.PathEscape(code) + "/conversions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return app, fmt.Errorf("build conversion request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cicada-spike")

	resp, err := f.client.Do(req)
	if err != nil {
		return app, fmt.Errorf("conversion request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return app, fmt.Errorf("read conversion response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		// The body of an error response holds no credentials, but it is not echoed
		// anyway: the status is enough to diagnose.
		return app, fmt.Errorf("GitHub answered %s to the conversion request", resp.Status)
	}
	if err := json.Unmarshal(body, &app); err != nil {
		return app, errors.New("conversion response is not valid JSON")
	}
	if app.ID == 0 || app.PEM == "" || app.Slug == "" {
		return app, errors.New("conversion response is missing the app id, slug or private key")
	}
	return app, nil
}

// saveCredentials stores the App's identity and private key in dir, owner-only.
// The spike uses files; the real tool will use the OS secret store.
func saveCredentials(dir string, app appCredentials) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict %s: %w", dir, err)
	}
	meta, err := json.MarshalIndent(map[string]any{
		"id":        app.ID,
		"slug":      app.Slug,
		"name":      app.Name,
		"client_id": app.ClientID,
		"html_url":  app.HTMLURL,
		"owner":     app.Owner.Login,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "app.json"), append(meta, '\n')); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "private-key.pem"), []byte(app.PEM))
}

// writeFileAtomic writes data to a temporary file beside path and renames it into place,
// so a crash never leaves a half-written file. The file is readable by its owner only.
func writeFileAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*") // created with mode 0600
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("flush %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save %s: %w", filepath.Base(path), err)
	}
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
