package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// maxCredFile and maxAPIResponse cap how much is read from disk and from GitHub.
const (
	maxCredFile    = 64 << 10
	maxAPIResponse = 1 << 20
)

var (
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// appCreds is the GitHub App identity written by spike S3.
type appCreds struct {
	ClientID   string
	Slug       string
	PrivateKey string // PEM; never logged
}

// target is where the scale set is registered: an organisation, or one repository.
type target struct {
	WebURL string // https://github.com
	Owner  string
	Repo   string // empty for an organisation
}

// ConfigURL is the form the scale set client expects.
func (t target) ConfigURL() string {
	if t.Repo == "" {
		return t.WebURL + "/" + t.Owner
	}
	return t.WebURL + "/" + t.Owner + "/" + t.Repo
}

func (t target) String() string {
	if t.Repo == "" {
		return t.Owner
	}
	return t.Owner + "/" + t.Repo
}

// parseTarget validates a GitHub URL of the form https://host/owner or https://host/owner/repo.
func parseTarget(raw string) (target, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return target{}, fmt.Errorf("%q must look like https://github.com/OWNER or https://github.com/OWNER/REPO", raw)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	t := target{WebURL: "https://" + u.Host}
	switch {
	case len(parts) == 1 && ownerPattern.MatchString(parts[0]):
		t.Owner = parts[0]
	case len(parts) == 2 && ownerPattern.MatchString(parts[0]) && repoPattern.MatchString(parts[1]) && !strings.HasPrefix(parts[1], "."):
		t.Owner, t.Repo = parts[0], parts[1]
	default:
		return target{}, fmt.Errorf("%q must look like https://github.com/OWNER or https://github.com/OWNER/REPO", raw)
	}
	return t, nil
}

// apiURLFor returns the REST API base for a web URL.
func apiURLFor(webURL string) string {
	if webURL == "https://github.com" {
		return "https://api.github.com"
	}
	return webURL + "/api/v3" // GitHub Enterprise Server
}

// loadAppCreds reads the App identity and private key that spike S3 stored in dir.
func loadAppCreds(dir string) (appCreds, error) {
	var creds appCreds
	meta, err := readSmallFile(filepath.Join(dir, "app.json"))
	if err != nil {
		return creds, fmt.Errorf("read app identity (run spike S3 first): %w", err)
	}
	var parsed struct {
		ClientID string `json:"client_id"`
		Slug     string `json:"slug"`
	}
	if err := json.Unmarshal(meta, &parsed); err != nil || parsed.ClientID == "" {
		return creds, errors.New("app.json is not valid or has no client_id")
	}

	keyPath := filepath.Join(dir, "private-key.pem")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(keyPath)
		if err != nil {
			return creds, fmt.Errorf("read private key: %w", err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return creds, fmt.Errorf("%s is readable by other users; restrict it to its owner (mode 600)", keyPath)
		}
	}
	key, err := readSmallFile(keyPath)
	if err != nil {
		return creds, fmt.Errorf("read private key: %w", err)
	}
	return appCreds{ClientID: parsed.ClientID, Slug: parsed.Slug, PrivateKey: string(key)}, nil
}

func readSmallFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCredFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCredFile {
		return nil, fmt.Errorf("%s is larger than expected", filepath.Base(path))
	}
	return data, nil
}

// appJWT signs the short-lived token a GitHub App uses to identify itself.
func appJWT(creds appCreds, now time.Time) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(creds.PrivateKey))
	if err != nil {
		return "", errors.New("the stored private key is not a valid RSA key")
	}
	claims := jwt.RegisteredClaims{
		Issuer:    creds.ClientID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)), // tolerate small clock differences
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign app token: %w", err)
	}
	return signed, nil
}

// errNotInstalled means the App exists but has no access to the target.
var errNotInstalled = errors.New("the App is not installed on the target")

// findInstallation looks up the App's installation ID for the target.
func findInstallation(ctx context.Context, client *http.Client, apiURL string, t target, appToken string) (int64, error) {
	endpoint := apiURL + "/orgs/" + url.PathEscape(t.Owner) + "/installation"
	if t.Repo != "" {
		endpoint = apiURL + "/repos/" + url.PathEscape(t.Owner) + "/" + url.PathEscape(t.Repo) + "/installation"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("build installation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cicada-spike")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("installation request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return 0, fmt.Errorf("read installation response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return 0, errNotInstalled
	default:
		return 0, fmt.Errorf("GitHub answered %s to the installation request", resp.Status)
	}
	var parsed struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.ID == 0 {
		return 0, errors.New("installation response has no id")
	}
	return parsed.ID, nil
}
