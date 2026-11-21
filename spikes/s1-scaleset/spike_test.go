package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/actions/scaleset"
	"github.com/golang-jwt/jwt/v5"
)

var quiet = slog.New(slog.DiscardHandler)

// ---- fakes ----

type fakeJobs struct {
	acquired [][]int64
	err      error
}

func (f *fakeJobs) AcquireJobs(_ context.Context, ids []int64) ([]int64, error) {
	f.acquired = append(f.acquired, ids)
	return ids, f.err
}

type fakeJIT struct{ err error }

func (f fakeJIT) GenerateJitRunnerConfig(_ context.Context, s *scaleset.RunnerScaleSetJitRunnerSetting, _ int) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &scaleset.RunnerScaleSetJitRunnerConfig{EncodedJITConfig: "jit-for-" + s.Name}, nil
}

type fakeBackend struct {
	mu       sync.Mutex
	started  map[string]string // name -> jit config
	removed  []string
	startErr error
}

func (f *fakeBackend) Start(_ context.Context, name, jit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.started[name] = jit
	return nil
}

func (f *fakeBackend) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, name)
	return nil
}

func newTestScaler(maxRunners int) (*scaler, *fakeJobs, *fakeBackend) {
	jobs := &fakeJobs{}
	be := &fakeBackend{started: map[string]string{}}
	n := 0
	return &scaler{
		log: quiet, jobs: jobs, jit: fakeJIT{}, backend: be,
		scaleSetID: 7, maxRunners: maxRunners,
		newName: func() string { n++; return fmt.Sprintf("runner-%d", n) },
		runners: map[string]struct{}{},
	}, jobs, be
}

func stats(assigned int) *scaleset.RunnerScaleSetStatistic {
	return &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: assigned}
}

// ---- scaler ----

func TestScaleStartsOneRunnerPerAssignedJobUpToMax(t *testing.T) {
	tests := []struct {
		name               string
		maxRunners         int
		assigned           int
		wantStartedRunners int
	}{
		{"nothing assigned", 3, 0, 0},
		{"below capacity", 3, 2, 2},
		{"at capacity", 3, 3, 3},
		{"above capacity is capped", 3, 10, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _, be := newTestScaler(tc.maxRunners)
			if err := s.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{Statistics: stats(tc.assigned)}); err != nil {
				t.Fatal(err)
			}
			if len(be.started) != tc.wantStartedRunners || s.count() != tc.wantStartedRunners {
				t.Errorf("started %d, tracked %d, want %d", len(be.started), s.count(), tc.wantStartedRunners)
			}
			for name, jit := range be.started {
				if jit != "jit-for-"+name {
					t.Errorf("runner %s got configuration %q", name, jit)
				}
			}
		})
	}
}

func TestScaleIsIdempotentWhenAMessageIsRedelivered(t *testing.T) {
	s, _, be := newTestScaler(5)
	msg := &scaleset.RunnerScaleSetMessage{Statistics: stats(2)}
	for range 3 {
		if err := s.Scale(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if len(be.started) != 2 {
		t.Errorf("started %d runners for 2 assigned jobs delivered 3 times", len(be.started))
	}
}

func TestScaleAcquiresEveryAvailableJob(t *testing.T) {
	s, jobs, _ := newTestScaler(2)
	msg := &scaleset.RunnerScaleSetMessage{
		JobAvailableMessages: []*scaleset.JobAvailable{
			{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 11}},
			{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 12}},
		},
	}
	if err := s.Scale(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(jobs.acquired) != 1 || len(jobs.acquired[0]) != 2 || jobs.acquired[0][0] != 11 || jobs.acquired[0][1] != 12 {
		t.Errorf("acquired = %v, want one call with [11 12]", jobs.acquired)
	}

	jobs.err = errors.New("boom")
	if err := s.Scale(context.Background(), msg); err == nil {
		t.Error("an acquire failure must be returned so the message is redelivered")
	}
}

func TestScaleNilMessageDoesNothing(t *testing.T) {
	s, jobs, be := newTestScaler(2)
	if err := s.Scale(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(jobs.acquired) != 0 || len(be.started) != 0 {
		t.Error("a nil message caused work")
	}
}

func TestCompletedJobFreesCapacityAndRemovesRunner(t *testing.T) {
	s, _, be := newTestScaler(1)
	ctx := context.Background()
	if err := s.Scale(ctx, &scaleset.RunnerScaleSetMessage{Statistics: stats(2)}); err != nil {
		t.Fatal(err)
	}
	done := &scaleset.RunnerScaleSetMessage{
		JobCompletedMessages: []*scaleset.JobCompleted{{RunnerName: "runner-1", Result: "succeeded"}},
		Statistics:           stats(1),
	}
	if err := s.Scale(ctx, done); err != nil {
		t.Fatal(err)
	}
	if len(be.removed) != 1 || be.removed[0] != "runner-1" {
		t.Errorf("removed = %v, want [runner-1]", be.removed)
	}
	if _, ok := be.started["runner-2"]; !ok || s.count() != 1 {
		t.Errorf("the freed slot was not reused: started %v, tracked %d", be.started, s.count())
	}

	// A completion for a runner we never started (for example after a restart) is ignored.
	unknown := &scaleset.RunnerScaleSetMessage{JobCompletedMessages: []*scaleset.JobCompleted{{RunnerName: "stranger"}}}
	if err := s.Scale(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if len(be.removed) != 1 {
		t.Errorf("removed a runner that was never tracked: %v", be.removed)
	}
}

func TestRunnerExitFreesCapacity(t *testing.T) {
	s, _, be := newTestScaler(1)
	ctx := context.Background()
	if err := s.Scale(ctx, &scaleset.RunnerScaleSetMessage{Statistics: stats(1)}); err != nil {
		t.Fatal(err)
	}
	s.runnerExited("runner-1")
	s.runnerExited("runner-1") // a second report must be harmless
	if err := s.Scale(ctx, &scaleset.RunnerScaleSetMessage{Statistics: stats(1)}); err != nil {
		t.Fatal(err)
	}
	if len(be.started) != 2 {
		t.Errorf("started %d runners, want a replacement after the exit", len(be.started))
	}
}

func TestFailedStartIsReportedAndNotTracked(t *testing.T) {
	s, _, be := newTestScaler(2)
	be.startErr = errors.New("no docker")
	if err := s.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{Statistics: stats(1)}); err == nil {
		t.Fatal("want an error")
	}
	if s.count() != 0 {
		t.Errorf("a runner that failed to start is still tracked")
	}

	s2, _, _ := newTestScaler(2)
	s2.jit = fakeJIT{err: errors.New("denied")}
	if err := s2.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{Statistics: stats(1)}); err == nil || s2.count() != 0 {
		t.Errorf("configuration failure: err %v, tracked %d", err, s2.count())
	}
}

func TestShutdownRemovesEveryRunner(t *testing.T) {
	s, _, be := newTestScaler(3)
	if err := s.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{Statistics: stats(3)}); err != nil {
		t.Fatal(err)
	}
	s.shutdown(context.Background())
	if len(be.removed) != 3 || s.count() != 0 {
		t.Errorf("removed %d, still tracked %d", len(be.removed), s.count())
	}
}

// ---- input validation ----

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in        string
		wantURL   string
		wantError bool
	}{
		{"https://github.com/acme", "https://github.com/acme", false},
		{"https://github.com/acme/widgets/", "https://github.com/acme/widgets", false},
		{"https://ghe.example.com/acme/my.repo", "https://ghe.example.com/acme/my.repo", false},
		{"http://github.com/acme", "", true},
		{"https://github.com/", "", true},
		{"https://github.com/acme/widgets/extra", "", true},
		{"https://github.com/acme/..", "", true},
		{"https://github.com/acme?x=1", "", true},
		{"https://user@github.com/acme", "", true},
		{"github.com/acme", "", true},
	}
	for _, tc := range tests {
		got, err := parseTarget(tc.in)
		if (err != nil) != tc.wantError {
			t.Errorf("parseTarget(%q) error = %v, wantError %v", tc.in, err, tc.wantError)
			continue
		}
		if err == nil && got.ConfigURL() != tc.wantURL {
			t.Errorf("parseTarget(%q).ConfigURL() = %q, want %q", tc.in, got.ConfigURL(), tc.wantURL)
		}
	}
}

func TestParseLabels(t *testing.T) {
	got, err := parseLabels(" cicada , linux,,x64 ")
	if err != nil || strings.Join(labelNames(got), "|") != "cicada|linux|x64" {
		t.Errorf("labels = %v, err %v", labelNames(got), err)
	}
	for _, bad := range []string{"", " , ", "has space", "semi;colon"} {
		if _, err := parseLabels(bad); err == nil {
			t.Errorf("parseLabels(%q) should fail", bad)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"Naseebs-MacBook.local": "naseebs-macbook-local",
		"":                      "host",
		"!!!":                   "host",
		strings.Repeat("a", 50): strings.Repeat("a", 30),
	} {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDockerBackendRejectsUnsafeNames(t *testing.T) {
	d := &dockerBackend{image: "x", log: quiet, onExit: func(string) {}}
	for _, name := range []string{"", "--privileged", "a b", "a;b", "UPPER"} {
		if err := d.Start(context.Background(), name, "jit"); err == nil {
			t.Errorf("Start accepted name %q", name)
		}
		if err := d.Remove(context.Background(), name); err == nil {
			t.Errorf("Remove accepted name %q", name)
		}
	}
}

// ---- app authentication ----

// testKeyPEM makes a throwaway RSA key for tests. It is generated at run time and never stored.
func testKeyPEM(t *testing.T) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block)), &key.PublicKey
}

func TestAppJWT(t *testing.T) {
	keyPEM, pub := testKeyPEM(t)
	now := time.Now()
	signed, err := appJWT(appCreds{ClientID: "Iv1.test", PrivateKey: keyPEM}, now)
	if err != nil {
		t.Fatal(err)
	}
	var claims jwt.RegisteredClaims
	if _, err := jwt.ParseWithClaims(signed, &claims, func(*jwt.Token) (any, error) { return pub, nil },
		jwt.WithValidMethods([]string{"RS256"})); err != nil {
		t.Fatalf("token does not verify: %v", err)
	}
	if claims.Issuer != "Iv1.test" {
		t.Errorf("issuer = %q", claims.Issuer)
	}
	if life := claims.ExpiresAt.Sub(claims.IssuedAt.Time); life > 10*time.Minute {
		t.Errorf("token lifetime %s exceeds GitHub's 10 minute limit", life)
	}

	if _, err := appJWT(appCreds{ClientID: "x", PrivateKey: "not a key"}, now); err == nil || strings.Contains(err.Error(), "not a key") {
		t.Errorf("bad key: err = %v (must fail without echoing the key)", err)
	}
}

func TestFindInstallation(t *testing.T) {
	var gotPath, gotAuth string
	status, body := http.StatusOK, `{"id": 99}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	ctx := context.Background()

	id, err := findInstallation(ctx, srv.Client(), srv.URL, target{Owner: "acme", Repo: "widgets"}, "tok")
	if err != nil || id != 99 || gotPath != "/repos/acme/widgets/installation" || gotAuth != "Bearer tok" {
		t.Errorf("repo lookup: id %d, err %v, path %q, auth %q", id, err, gotPath, gotAuth)
	}
	if _, err := findInstallation(ctx, srv.Client(), srv.URL, target{Owner: "acme"}, "tok"); err != nil || gotPath != "/orgs/acme/installation" {
		t.Errorf("org lookup: err %v, path %q", err, gotPath)
	}

	status = http.StatusNotFound
	if _, err := findInstallation(ctx, srv.Client(), srv.URL, target{Owner: "acme"}, "tok"); !errors.Is(err, errNotInstalled) {
		t.Errorf("404: err = %v, want errNotInstalled", err)
	}
	status, body = http.StatusUnauthorized, `{"message":"secret detail"}`
	if _, err := findInstallation(ctx, srv.Client(), srv.URL, target{Owner: "acme"}, "tok"); err == nil || strings.Contains(err.Error(), "secret detail") {
		t.Errorf("401: err = %v (must fail without echoing the body)", err)
	}
	status, body = http.StatusOK, `{}`
	if _, err := findInstallation(ctx, srv.Client(), srv.URL, target{Owner: "acme"}, "tok"); err == nil {
		t.Error("a response without an id must fail")
	}
}

func TestLoadAppCreds(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := loadAppCreds(dir); err == nil {
		t.Error("missing files must fail")
	}

	write("app.json", `{"client_id":"Iv1.test","slug":"cicada-test"}`, 0o600)
	write("private-key.pem", "fake-key-material", 0o600)
	creds, err := loadAppCreds(dir)
	if err != nil || creds.ClientID != "Iv1.test" || creds.Slug != "cicada-test" || creds.PrivateKey != "fake-key-material" {
		t.Fatalf("creds = %+v, err %v", creds.ClientID, err)
	}

	if runtime.GOOS != "windows" {
		write("private-key.pem", "fake-key-material", 0o644)
		if _, err := loadAppCreds(dir); err == nil || !strings.Contains(err.Error(), "readable by other users") {
			t.Errorf("a world-readable key must be refused, got %v", err)
		}
		write("private-key.pem", "fake-key-material", 0o600)
	}

	write("app.json", `{"slug":"no-client-id"}`, 0o600)
	if _, err := loadAppCreds(dir); err == nil {
		t.Error("app.json without client_id must fail")
	}
}
