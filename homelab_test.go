package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseAllowedEmails(t *testing.T) {
	if parseAllowedEmails("  , ") != nil {
		t.Fatal("empty list must allow all (nil)")
	}
	got := parseAllowedEmails(" Owner@Example.com ,other@example.com")
	if !got["owner@example.com"] || !got["other@example.com"] || len(got) != 2 {
		t.Fatalf("unexpected allowlist: %v", got)
	}
}

func TestEmailAllowed(t *testing.T) {
	um := NewUserManager()
	if !um.emailAllowed("anyone@example.com") {
		t.Fatal("no allowlist must allow every account")
	}
	um.allowedEmails = parseAllowedEmails("owner@example.com")
	if !um.emailAllowed(" OWNER@example.com ") {
		t.Fatal("allowlisted account must be case and space insensitive")
	}
	if um.emailAllowed("stranger@example.com") {
		t.Fatal("account outside the allowlist must be refused")
	}
}

func TestGetOrCreateUserRefusesAccountOutsideAllowlist(t *testing.T) {
	um := NewUserManager()
	um.allowedEmails = parseAllowedEmails("owner@example.com")
	called := false
	um.newUser = func(email, password string, proxyURL *url.URL) (*ThingsMCP, error) {
		called = true
		return &ThingsMCP{}, nil
	}
	if _, err := um.GetOrCreateUser("stranger@example.com", "pw"); err == nil {
		t.Fatal("expected refusal for an account outside the allowlist")
	}
	if called {
		t.Fatal("refused account must not reach Things Cloud")
	}
}

func TestAuthorizePostRefusesAccountOutsideAllowlistWithoutVerify(t *testing.T) {
	um := NewUserManager()
	um.allowedEmails = parseAllowedEmails("owner@example.com")
	o := NewOAuthServer(um, t.TempDir())
	defer o.db.Close()
	form := url.Values{"email": {"stranger@example.com"}, "password": {"pw"}}
	req := httptest.NewRequest(http.MethodPost, "/authorize?client_id=x&redirect_uri=https%3A%2F%2Fexample.com&state=s&code_challenge=c", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	o.handleAuthorizePost(rec, req)
	if !strings.Contains(rec.Body.String(), "Invalid Things Cloud credentials") {
		t.Fatalf("expected generic credential error, got status %d", rec.Code)
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if len(o.authCodes) != 0 {
		t.Fatal("no auth code may be issued for a refused account")
	}
}

func TestRefreshRefusesAccountRemovedFromAllowlist(t *testing.T) {
	t.Setenv("CREDENTIALS_SECRET", "")
	um := NewUserManager()
	um.allowedEmails = parseAllowedEmails("owner@example.com")
	o := NewOAuthServer(um, t.TempDir())
	defer o.db.Close()
	const removed = "former@example.com"
	if err := o.persistCredential(removed, "pw"); err != nil {
		t.Fatal(err)
	}
	rt := &RefreshToken{Token: "refresh-removed", Email: removed, Password: "pw", ClientID: "c", ExpiresAt: time.Now().Add(time.Hour)}
	if err := o.persistRefreshToken(rt); err != nil {
		t.Fatal(err)
	}
	o.mu.Lock()
	o.refreshTokens[refreshTokenKey(rt.Token)] = rt
	o.credentials[removed] = "pw"
	o.mu.Unlock()

	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt.Token}}
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	o.handleRefreshTokenGrant(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_grant") {
		t.Fatalf("expected invalid_grant, got %d %s", rec.Code, rec.Body.String())
	}
	o.mu.RLock()
	_, tokenLeft := o.refreshTokens[refreshTokenKey(rt.Token)]
	_, credLeft := o.credentials[removed]
	o.mu.RUnlock()
	if tokenLeft || credLeft {
		t.Fatal("refused account must lose its refresh token and stored password")
	}
	var n int
	o.db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens`).Scan(&n)
	var c int
	o.db.QueryRow(`SELECT COUNT(*) FROM credentials WHERE email=?`, removed).Scan(&c)
	if n != 0 || c != 0 {
		t.Fatalf("database still holds %d refresh token(s) and %d credential(s)", n, c)
	}
}

func TestListenAddrDefaultsToLoopback(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	if got := listenAddr("28063"); got != "127.0.0.1:28063" {
		t.Fatalf("default listen addr = %q", got)
	}
	t.Setenv("LISTEN_ADDR", "0.0.0.0")
	if got := listenAddr("8080"); got != "0.0.0.0:8080" {
		t.Fatalf("override listen addr = %q", got)
	}
}

func TestClientIPTrustsTunnelHeaderOnlyFromLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/authorize", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("loopback peer with tunnel header = %q", got)
	}
	r.RemoteAddr = "198.51.100.7:5555"
	if got := clientIP(r); got != "198.51.100.7" {
		t.Fatalf("non-loopback peer must not be able to spoof its address, got %q", got)
	}
}

func TestAttemptLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newAttemptLimiter(2, 3, time.Minute)
	l.now = func() time.Time { return now }
	if !l.allow("a") || !l.allow("a") {
		t.Fatal("first attempts must pass")
	}
	if l.allow("a") {
		t.Fatal("per-key limit must apply")
	}
	if !l.allow("b") {
		t.Fatal("other key must pass")
	}
	if l.allow("c") {
		t.Fatal("global limit must apply")
	}
	now = now.Add(time.Minute)
	if !l.allow("a") {
		t.Fatal("limit must reset after the window")
	}
}

func TestLimitPOSTOnlyLimitsPost(t *testing.T) {
	l := newAttemptLimiter(1, 10, time.Hour)
	h := limitPOST(l, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	for i, tc := range []struct {
		method string
		want   int
	}{{http.MethodGet, 200}, {http.MethodPost, 200}, {http.MethodPost, 429}, {http.MethodGet, 200}} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(tc.method, "/authorize", nil))
		if rec.Code != tc.want {
			t.Fatalf("request %d (%s) = %d, want %d", i, tc.method, rec.Code, tc.want)
		}
	}
}
