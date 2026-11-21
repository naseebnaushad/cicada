package main

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakePEM stands in for a private key. It is not a real key.
const fakePEM = "-----BEGIN FAKE TEST KEY-----\nnot-a-real-key\n-----END FAKE TEST KEY-----\n"

const testAddr = "127.0.0.1:54321"

// newTestFlow returns a flow pointed at a fake GitHub API.
func newTestFlow(t *testing.T, cfg config, api http.Handler) *flow {
	t.Helper()
	if api == nil {
		api = http.NotFoundHandler()
	}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	if cfg.GitHubURL == "" {
		cfg.GitHubURL = "https://github.com"
	}
	cfg.APIURL = srv.URL
	if cfg.OutDir == "" {
		cfg.OutDir = filepath.Join(t.TempDir(), "creds")
	}
	f, err := newFlow(cfg, testAddr)
	if err != nil {
		t.Fatalf("newFlow: %v", err)
	}
	return f
}

// get sends a request to the flow's handler as a browser on the loopback address would.
func get(f *flow, target, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	f.handler().ServeHTTP(rec, req)
	return rec
}

func TestNewFlowValidation(t *testing.T) {
	base := config{GitHubURL: "https://github.com", APIURL: "https://api.github.com", OutDir: "x"}
	tests := []struct {
		name    string
		mutate  func(*config)
		wantErr string
	}{
		{"defaults are valid", func(*config) {}, ""},
		{"org name with slash", func(c *config) { c.Org = "acme/../x" }, "not a valid organisation"},
		{"unknown scope", func(c *config) { c.Scope = "enterprise" }, "must be org or repo"},
		{"name too long", func(c *config) { c.Name = strings.Repeat("a", 35) }, "app name"},
		{"plain http GitHub", func(c *config) { c.GitHubURL = "http://github.com" }, "must be an https URL"},
		{"plain http API", func(c *config) { c.APIURL = "http://api.example.com" }, "must be an https URL"},
		{"empty output folder", func(c *config) { c.OutDir = "" }, "output folder"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			_, err := newFlow(cfg, testAddr)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestManifestIsLeastPrivilege(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config
		wantPerms map[string]string
		wantPath  string
	}{
		{
			name:      "personal account defaults to repo scope",
			cfg:       config{},
			wantPerms: map[string]string{"administration": "write", "metadata": "read"},
			wantPath:  "https://github.com/settings/apps/new?state=",
		},
		{
			name:      "organisation defaults to org scope",
			cfg:       config{Org: "acme"},
			wantPerms: map[string]string{"organization_self_hosted_runners": "write", "metadata": "read"},
			wantPath:  "https://github.com/organizations/acme/settings/apps/new?state=",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFlow(t, tc.cfg, nil)
			m := f.manifest()

			if len(m.DefaultPermissions) != len(tc.wantPerms) {
				t.Fatalf("permissions = %v, want exactly %v", m.DefaultPermissions, tc.wantPerms)
			}
			for k, v := range tc.wantPerms {
				if m.DefaultPermissions[k] != v {
					t.Errorf("permission %s = %q, want %q", k, m.DefaultPermissions[k], v)
				}
			}
			if m.Public {
				t.Error("the App must be private")
			}
			if len(m.DefaultEvents) != 0 {
				t.Errorf("events = %v, want none", m.DefaultEvents)
			}
			if want := "http://" + testAddr + "/callback"; m.RedirectURL != want {
				t.Errorf("redirect_url = %q, want %q", m.RedirectURL, want)
			}
			if got := f.registerURL(); !strings.HasPrefix(got, tc.wantPath) || !strings.HasSuffix(got, f.state) {
				t.Errorf("registerURL = %q, want prefix %q and the state as suffix", got, tc.wantPath)
			}

			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "hook_attributes") {
				t.Error("the manifest must not configure a webhook")
			}
		})
	}
}

func TestStartPagePostsManifestToGitHub(t *testing.T) {
	f := newTestFlow(t, config{Name: "cicada-test"}, nil)
	rec := get(f, "/", testAddr)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := html.UnescapeString(rec.Body.String())
	for _, want := range []string{
		`action="` + f.registerURL() + `"`,
		`"name":"cicada-test"`,
		`"redirect_url":"http://` + testAddr + `/callback"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("start page is missing %q", want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "form-action https://github.com") {
		t.Errorf("Content-Security-Policy = %q", csp)
	}
}

func TestRequestsForAnotherHostAreRefused(t *testing.T) {
	f := newTestFlow(t, config{}, nil)
	for _, target := range []string{"/", "/callback?code=abc&state=" + f.state} {
		if rec := get(f, target, "evil.example:54321"); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s with a foreign Host: status = %d, want 403", target, rec.Code)
		}
	}
}

func TestCallbackRejectsBadInput(t *testing.T) {
	apiCalled := false
	f := newTestFlow(t, config{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { apiCalled = true }))

	tests := []struct{ name, target string }{
		{"wrong state", "/callback?code=abc123&state=wrong"},
		{"missing state", "/callback?code=abc123"},
		{"missing code", "/callback?state=" + f.state},
		{"code with path characters", "/callback?code=..%2F..%2Fuser&state=" + f.state},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if rec := get(f, tc.target, testAddr); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
	if apiCalled {
		t.Error("the GitHub API was called for a rejected callback")
	}
	select {
	case out := <-f.done:
		t.Errorf("flow finished on rejected input: %+v", out.err)
	default:
	}
}

func TestCallbackStoresCredentialsOwnerOnly(t *testing.T) {
	var gotPath, gotMethod string
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 4242, "slug": "cicada-test", "name": "cicada-test", "client_id": "Iv1.test",
			"html_url": "https://github.com/apps/cicada-test", "pem": fakePEM,
			"webhook_secret": "unused", "client_secret": "unused",
			"owner": map[string]string{"login": "octo-test"},
		})
	})
	f := newTestFlow(t, config{}, api)

	rec := get(f, "/callback?code=abc123&state="+f.state, testAddr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}
	if gotMethod != http.MethodPost || gotPath != "/app-manifests/abc123/conversions" {
		t.Errorf("API call = %s %s, want POST /app-manifests/abc123/conversions", gotMethod, gotPath)
	}
	if strings.Contains(rec.Body.String(), "not-a-real-key") {
		t.Error("the private key was sent to the browser")
	}
	if !strings.Contains(rec.Body.String(), "https://github.com/apps/cicada-test/installations/new") {
		t.Error("the result page is missing the install link")
	}

	out := <-f.done
	if out.err != nil || out.app.ID != 4242 || out.app.Owner.Login != "octo-test" {
		t.Fatalf("outcome = %+v, err %v", out.app.ID, out.err)
	}

	key, err := os.ReadFile(filepath.Join(f.cfg.OutDir, "private-key.pem"))
	if err != nil || string(key) != fakePEM {
		t.Fatalf("stored key mismatch (err %v)", err)
	}
	meta, err := os.ReadFile(filepath.Join(f.cfg.OutDir, "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"not-a-real-key", "webhook_secret", "client_secret", "unused"} {
		if strings.Contains(string(meta), secret) {
			t.Errorf("app.json contains %q", secret)
		}
	}
	if runtime.GOOS != "windows" { // Windows has no Unix permission bits
		for name, want := range map[string]os.FileMode{"": 0o700, "private-key.pem": 0o600, "app.json": 0o600} {
			info, err := os.Stat(filepath.Join(f.cfg.OutDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Errorf("%q mode = %o, want %o", name, got, want)
			}
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(f.cfg.OutDir, ".tmp-*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}

	// The code is single-use: a replayed callback must not reach GitHub again.
	gotPath = ""
	if rec := get(f, "/callback?code=abc123&state="+f.state, testAddr); rec.Code != http.StatusConflict {
		t.Errorf("replayed callback: status = %d, want 409", rec.Code)
	}
	if gotPath != "" {
		t.Error("a replayed callback called the GitHub API again")
	}
}

func TestCallbackHidesAPIFailureDetails(t *testing.T) {
	tests := []struct {
		name    string
		api     http.HandlerFunc
		wantErr string
	}{
		{
			name: "GitHub refuses the code",
			api: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"message":"internal detail"}`)
			},
			wantErr: "404",
		},
		{
			name: "response without a key",
			api: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"id":1,"slug":"x"}`)
			},
			wantErr: "missing",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFlow(t, config{}, tc.api)
			rec := get(f, "/callback?code=abc123&state="+f.state, testAddr)

			if rec.Code != http.StatusBadGateway {
				t.Errorf("status = %d, want 502", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "internal detail") || strings.Contains(rec.Body.String(), "404") {
				t.Errorf("the browser page leaks failure details: %s", rec.Body)
			}
			out := <-f.done
			if out.err == nil || !strings.Contains(out.err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", out.err, tc.wantErr)
			}
			if _, err := os.Stat(filepath.Join(f.cfg.OutDir, "private-key.pem")); !os.IsNotExist(err) {
				t.Error("a key file exists after a failed exchange")
			}
		})
	}
}
