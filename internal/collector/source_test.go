package collector

import (
	"context"
	"cyd-token-usage-meter/internal/usage"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Unix(1800000000, 0)

const codexExample = `{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":15,"reset_at":1800015000},"secondary_window":{"limit_window_seconds":604800,"used_percent":36,"reset_at":1800439200}}}`
const claudeExample = `{"five_hour":{"utilization":28,"resets_at":"2027-01-15T10:14:00Z"},"seven_day":{"utilization":62,"resets_at":"2027-01-18T16:00:00Z"},"seven_day_sonnet":{"utilization":99,"resets_at":null}}`

func TestProviderWindows(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		raw := claudeExample
		if provider == "codex" {
			raw = codexExample
		}
		p, err := parseProvider(provider, []byte(raw), testNow)
		if err != nil || p.ObservedAt != testNow.Unix() || p.FiveHour == nil || p.Weekly == nil {
			t.Fatal(provider, p, err)
		}
		for _, bad := range []string{`{}`, `null`, raw + `{}`, strings.Replace(raw, `"utilization":28`, `"utilization":true`, 1), strings.Repeat(" ", usage.MaxSourceBytes+1)} {
			if bad == raw {
				continue
			}
			if _, err := parseProvider(provider, []byte(bad), testNow); err == nil {
				t.Fatal("accepted", provider, bad[:min(80, len(bad))])
			}
		}
	}
	codex, err := parseProvider("codex", []byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":0,"reset_at":1800439200},"secondary_window":null},"additional_rate_limits":[{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":99}}}]}`), testNow)
	if err != nil || codex.FiveHour != nil || codex.Weekly.Remaining != 100 {
		t.Fatal("weekly-only or wrong Codex bucket")
	}
	claude, err := parseProvider("claude", []byte(`{"five_hour":null,"seven_day":{"utilization":0,"resets_at":null}}`), testNow)
	if err != nil || claude.FiveHour != nil || claude.Weekly.ResetAt != 0 || claude.Weekly.Remaining != 100 {
		t.Fatal("null/unused Claude window")
	}
	for _, raw := range []string{
		`{"rate_limit":{}}`,
		`{"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":1,"reset_at":1800439200},"secondary_window":{"limit_window_seconds":604800,"used_percent":2,"reset_at":1800439200}}}`,
		strings.Replace(codexExample, `"used_percent":15`, `"used_percent":-1`, 1),
		strings.Replace(codexExample, `"used_percent":15`, `"used_percent":null`, 1),
		strings.Replace(codexExample, `"reset_at":1800015000`, `"reset_at":1.5`, 1),
		strings.Replace(codexExample, `"rate_limit":`, `"rate_limit":{},"rate_limit":`, 1),
	} {
		if _, err := parseProvider("codex", []byte(raw), testNow); err == nil {
			t.Fatal("accepted invalid Codex source")
		}
	}
}

func TestAvailableResets(t *testing.T) {
	for _, value := range []string{"null", "0", "2", "2147483647", "-1", "2147483648", "1.5", `"2"`, "true", "1e999"} {
		raw := strings.TrimSuffix(codexExample, "}") + `,"rate_limit_reset_credits":{"available_count":` + value + `,"credits":[{},{}]},"credits":{"balance":"99"}}`
		p, err := parseProvider("codex", []byte(raw), testNow)
		if err != nil || p.Weekly.Remaining != 64 {
			t.Fatal("optional reset count broke usage", value, err)
		}
		want := map[string]int64{"0": 0, "2": 2, "2147483647": 2147483647}
		if n, known := want[value]; known {
			if p.ResetsAvailable == nil || *p.ResetsAvailable != n {
				t.Fatal("wrong reset count", value)
			}
		} else if p.ResetsAvailable != nil {
			t.Fatal("invented reset count", value)
		}
	}
	for _, provider := range []string{"claude", "codex"} {
		raw := codexExample
		if provider == "claude" {
			raw = claudeExample
		}
		p, err := parseProvider(provider, []byte(raw), testNow)
		if err != nil || p.ResetsAvailable != nil {
			t.Fatal("missing count must stay unknown")
		}
	}
	old := usage.Provider{ResetsAvailable: new(int64(2))}
	if apply(old, usage.Provider{}, errSourceUnavailable).ResetsAvailable == nil {
		t.Fatal("lost cached count on failure")
	}
	if apply(old, usage.Provider{}, nil).ResetsAvailable != nil {
		t.Fatal("reused missing count after successful refresh")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testAuth(t *testing.T, provider string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	var value any
	if provider == "claude" {
		value = map[string]any{"claudeAiOauth": map[string]any{"accessToken": "synthetic-claude-token", "expiresAt": testNow.Add(time.Hour).UnixMilli()}}
	} else {
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1800003600}`))
		value = map[string]any{"auth_mode": "chatgpt", "tokens": map[string]string{"access_token": "test." + payload + ".signature", "account_id": "synthetic-account"}}
	}
	raw, _ := json.Marshal(value)
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("auth fixture")
	}
	return path
}
func TestCredentialsAndFetch(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		path := testAuth(t, provider)
		client := newClient(time.Second)
		defer client.CloseIdleConnections()
		if client.CheckRedirect(nil, nil) != http.ErrUseLastResponse || client.Transport.(*http.Transport).Proxy != nil {
			t.Fatal("credential redirection enabled")
		}
		raw := claudeExample
		wantURL := claudeURL
		if provider == "codex" {
			raw = codexExample
			wantURL = codexURL
		}
		for _, status := range []int{200, 401, 403, 429, 500} {
			client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != wantURL || r.Method != "GET" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
					t.Fatal("credential destination")
				}
				if provider == "claude" && r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
					t.Fatal("Claude beta header")
				}
				if provider == "codex" && r.Header.Get("ChatGPT-Account-Id") != "synthetic-account" {
					t.Fatal("Codex account")
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}, nil
			})
			p, err := fetch(context.Background(), client, provider, path, []byte("synthetic-installation-key"), func() time.Time { return testNow })
			if status == 200 && (err != nil || p.ObservedAt != testNow.Unix() || !p.Valid()) {
				t.Fatal(provider, p, err)
			}
			if status != 200 && err == nil {
				t.Fatal("accepted failed response")
			}
			if status == 429 && err != errRateLimited {
				t.Fatal("missing rate limit backoff")
			}
			if strings.Contains(p.Scope, "synthetic") || len(p.Scope) != 64 {
				t.Fatal("identity leak")
			}
		}
		client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
		if _, err := fetch(context.Background(), client, provider, path, []byte("key"), func() time.Time { return testNow }); err != errSourceTimeout {
			t.Fatal("timeout classification")
		}
	}
	for _, raw := range []string{`{}`, `{"claudeAiOauth":{"accessToken":"x\nx","expiresAt":1800003600000}}`, `{"claudeAiOauth":{"accessToken":"x","expiresAt":1}}`} {
		if _, err := parseClaudeCredentials([]byte(raw), testNow); err == nil {
			t.Fatal("invalid Claude auth")
		}
	}
}
func TestIndependentFailureAndBackoff(t *testing.T) {
	old := usage.Provider{Scope: strings.Repeat("a", 64), ObservedAt: testNow.Unix(), Weekly: &usage.Window{Remaining: 64}}
	stale := apply(old, usage.Provider{}, errAuthFailed)
	if stale.Weekly.Remaining != 64 || stale.SourceError != "auth_failed" || stale.Status(testNow.Unix(), 900) != "stale" {
		t.Fatal("lost last known usage")
	}
	changed := apply(old, usage.Provider{Scope: strings.Repeat("b", 64)}, errSourceUnavailable)
	if changed.ObservedAt != 0 || changed.Weekly != nil {
		t.Fatal("mixed account cache")
	}
	good := old
	good.Weekly = &usage.Window{Remaining: 50}
	if got := apply(stale, good, nil); got.SourceError != "" || got.Weekly.Remaining != 50 {
		t.Fatal("recovery")
	}
	if apply(old, usage.Provider{}, errors.New("private token")).SourceError != "source_unavailable" {
		t.Fatal("raw error escaped")
	}
	if retryDelay(60*time.Second, 1) != time.Minute || retryDelay(60*time.Second, 10) != 30*time.Minute {
		t.Fatal("unbounded retry")
	}
}
