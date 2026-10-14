package collector

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cyd-token-usage-meter/internal/usage"
)

const codexURL = "https://chatgpt.com/backend-api/wham/usage"
const claudeURL = "https://api.anthropic.com/api/oauth/usage"

var (
	errAuthMissing       = errors.New("auth_missing")
	errAuthFailed        = errors.New("auth_failed")
	errSourceTimeout     = errors.New("source_timeout")
	errSourceUnavailable = errors.New("source_unavailable")
	errSourceInvalid     = usage.ErrInvalid
	errRateLimited       = errors.New("rate_limited")
)

type credentials struct{ access, account string }

func safeHeader(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func readCodex(path string, now time.Time) (credentials, error) {
	b, err := usage.ReadFile(path, usage.MaxAuthBytes)
	if err != nil {
		return credentials{}, errAuthMissing
	}
	var auth struct {
		Mode   string `json:"auth_mode"`
		Tokens struct {
			Access  string `json:"access_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if usage.StrictJSON(b, &auth, false) != nil || auth.Mode != "chatgpt" || !safeHeader(auth.Tokens.Access) || !safeHeader(auth.Tokens.Account) {
		return credentials{}, errAuthMissing
	}
	parts := strings.Split(auth.Tokens.Access, ".")
	if len(parts) != 3 {
		return credentials{}, errAuthMissing
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Expires int64 `json:"exp"`
	}
	if err != nil || usage.StrictJSON(payload, &claims, false) != nil || claims.Expires <= 0 {
		return credentials{}, errAuthMissing
	}
	if claims.Expires <= now.Add(30*time.Second).Unix() {
		return credentials{}, errAuthFailed
	}
	return credentials{auth.Tokens.Access, auth.Tokens.Account}, nil
}

func parseClaudeCredentials(raw []byte, now time.Time) (credentials, error) {
	var auth struct {
		OAuth struct {
			Access  string `json:"accessToken"`
			Expires int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if len(raw) > usage.MaxAuthBytes || usage.StrictJSON(raw, &auth, false) != nil || !safeHeader(auth.OAuth.Access) || auth.OAuth.Expires <= 0 {
		return credentials{}, errAuthMissing
	}
	if auth.OAuth.Expires <= now.Add(30*time.Second).UnixMilli() {
		return credentials{}, errAuthFailed
	}
	// A token change invalidates the cached scope; no raw account identity is persisted.
	return credentials{access: auth.OAuth.Access, account: auth.OAuth.Access}, nil
}

func keychain(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errAuthMissing
	}
	cmd.Stderr = io.Discard
	if cmd.Start() != nil {
		return nil, errAuthMissing
	}
	b, readErr := usage.BoundedRead(stdout, usage.MaxAuthBytes)
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	if err := cmd.Wait(); err != nil || readErr != nil {
		return nil, errAuthMissing
	}
	return b, nil
}

func readClaude(ctx context.Context, path string, now time.Time) (credentials, error) {
	if path == "auto" {
		if runtime.GOOS == "darwin" {
			// Read-only: Claude Code continues to own sign-in and token refresh.
			if raw, err := keychain(ctx); err == nil {
				return parseClaudeCredentials(raw, now)
			}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return credentials{}, errAuthMissing
		}
		path = filepath.Join(home, ".claude", ".credentials.json")
	}
	b, err := usage.ReadFile(path, usage.MaxAuthBytes)
	if err != nil {
		return credentials{}, errAuthMissing
	}
	return parseClaudeCredentials(b, now)
}

func scopeFor(key []byte, provider, account string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(provider + ":" + account))
	return hex.EncodeToString(m.Sum(nil))
}

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{Proxy: nil, DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout,
			DialContext:     (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
			MaxConnsPerHost: 1, MaxIdleConns: 2, IdleConnTimeout: 90 * time.Second}}
}

func sourceFailure(err error) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
		return errSourceTimeout
	}
	return errSourceUnavailable
}

func fetch(ctx context.Context, client *http.Client, provider, path string, key []byte, now func() time.Time) (usage.Provider, error) {
	var creds credentials
	var err error
	url := codexURL
	if provider == "claude" {
		url = claudeURL
		creds, err = readClaude(ctx, path, now())
	} else {
		creds, err = readCodex(path, now())
	}
	if err != nil {
		return usage.Provider{}, err
	}
	result := usage.Provider{Scope: scopeFor(key, provider, creds.account)}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return result, errSourceUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+creds.access)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cyd-token-usage-meter/0.1")
	if provider == "claude" {
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		req.Header.Set("ChatGPT-Account-Id", creds.account)
	}
	res, err := client.Do(req)
	if err != nil {
		return result, sourceFailure(err)
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return result, errAuthFailed
	}
	if res.StatusCode == 429 {
		return result, errRateLimited
	}
	if res.StatusCode != 200 {
		return result, errSourceUnavailable
	}
	if res.Header.Get("Content-Encoding") != "" {
		return result, errSourceInvalid
	}
	b, err := usage.BoundedRead(res.Body, usage.MaxSourceBytes)
	if err != nil {
		if ctx.Err() != nil {
			return result, sourceFailure(ctx.Err())
		}
		if sourceFailure(err) == errSourceTimeout {
			return result, errSourceTimeout
		}
		return result, errSourceInvalid
	}
	parsed, err := parseProvider(provider, b, now())
	parsed.Scope = result.Scope
	return parsed, err
}

func parseProvider(provider string, b []byte, completed time.Time) (usage.Provider, error) {
	var root map[string]any
	bad := func() (usage.Provider, error) { return usage.Provider{}, errSourceInvalid }
	if len(b) > usage.MaxSourceBytes || usage.StrictJSON(b, &root, false) != nil || root == nil || !usage.ValidEpoch(completed.Unix()) {
		return bad()
	}
	p := usage.Provider{ObservedAt: completed.Unix()}
	if provider == "claude" {
		for key, target := range map[string]**usage.Window{"five_hour": &p.FiveHour, "seven_day": &p.Weekly} {
			raw, present := root[key]
			if !present || raw == nil {
				continue
			}
			w := usage.Object(raw)
			used, ok := usage.Number(w["utilization"])
			if !ok || used < 0 {
				return bad()
			}
			reset := int64(0)
			value, present := w["resets_at"]
			if !present {
				return bad()
			}
			if value != nil {
				s, ok := value.(string)
				parsed, err := time.Parse(time.RFC3339Nano, s)
				if !ok || err != nil || !usage.ValidEpoch(parsed.Unix()) {
					return bad()
				}
				reset = parsed.Unix()
			}
			*target = &usage.Window{Remaining: math.Max(0, 100-used), ResetAt: reset}
		}
		// A successful empty/unsupported account response must identify at least one quota field.
		_, five := root["five_hour"]
		_, week := root["seven_day"]
		if !five && !week {
			return bad()
		}
	} else {
		// Only the authoritative earned-reset count; never credits balance or list length.
		summary := usage.Object(root["rate_limit_reset_credits"])
		if n, ok := usage.Integer(summary["available_count"]); ok && n >= 0 && n <= math.MaxInt32 {
			p.ResetsAvailable = &n
		}
		bucket := usage.Object(root["rate_limit"])
		_, primary := bucket["primary_window"]
		_, secondary := bucket["secondary_window"]
		if bucket == nil || (!primary && !secondary) {
			return bad()
		}
		for _, raw := range []any{bucket["primary_window"], bucket["secondary_window"]} {
			if raw == nil {
				continue
			}
			w := usage.Object(raw)
			duration, ok := usage.Integer(w["limit_window_seconds"])
			if !ok {
				return bad()
			}
			var target **usage.Window
			switch duration {
			case 18000:
				target = &p.FiveHour
			case 604800:
				target = &p.Weekly
			default:
				continue
			}
			if *target != nil {
				return bad()
			}
			used, ok := usage.Number(w["used_percent"])
			reset, valid := usage.Integer(w["reset_at"])
			if !ok || used < 0 || !valid || !usage.ValidEpoch(reset) {
				return bad()
			}
			*target = &usage.Window{Remaining: math.Max(0, 100-used), ResetAt: reset}
		}
	}
	return p, nil
}
