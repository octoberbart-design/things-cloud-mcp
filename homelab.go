package main

// Self-hosting hardening for a single-owner instance exposed through a
// reverse tunnel:
//
//   - ALLOWED_EMAILS: comma-separated Things Cloud accounts allowed to use the
//     server. Empty means every account (the original multi-user behavior).
//   - LISTEN_ADDR: interface to bind (default 127.0.0.1, so only a local tunnel
//     or reverse proxy can reach the server). Set 0.0.0.0 to listen everywhere.
//   - Per-client attempt limits on POST /authorize and POST /register.

import (
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// parseAllowedEmails returns nil when the list is empty (allow all).
func parseAllowedEmails(raw string) map[string]bool {
	allowed := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		email := strings.ToLower(strings.TrimSpace(part))
		if email != "" {
			allowed[email] = true
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

func (um *UserManager) emailAllowed(email string) bool {
	if um == nil || um.allowedEmails == nil {
		return true
	}
	return um.allowedEmails[strings.ToLower(strings.TrimSpace(email))]
}

func listenAddr(port string) string {
	host := strings.TrimSpace(os.Getenv("LISTEN_ADDR"))
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// clientIP returns the caller address. A tunnel or proxy header is trusted only
// when the TCP peer is loopback, i.e. the request came through a local proxy.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
			return v
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			return strings.TrimSpace(strings.Split(v, ",")[0])
		}
	}
	return host
}

// attemptLimiter is a fixed-window counter per key plus a global ceiling.
type attemptLimiter struct {
	perKey int
	global int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	start   time.Time
	total   int
	counter map[string]int
}

func newAttemptLimiter(perKey, global int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{perKey: perKey, global: global, window: window, now: time.Now, counter: map[string]int{}}
}

func (l *attemptLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.start) >= l.window {
		l.start = now
		l.total = 0
		l.counter = map[string]int{}
	}
	if l.total >= l.global || l.counter[key] >= l.perKey {
		return false
	}
	l.total++
	l.counter[key]++
	return true
}

// limitPOST applies the limiter to POST requests only; GET pages stay free.
func limitPOST(l *attemptLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "900")
			http.Error(w, "Too many attempts. Try again later.", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
