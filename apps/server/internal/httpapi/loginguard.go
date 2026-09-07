package httpapi

// Slowing down password guessing.
//
// The login endpoint had no limit of any kind: an attacker could try passwords
// against a known operator email as fast as the server would answer, for ever.
// PBKDF2 at 100k iterations is roughly 50ms of that answer, which is a real but
// accidental brake — and one that cuts both ways, since the same cost is an
// unauthenticated way to burn CPU on a 2GB box.
//
// This is deliberately a lockout rather than a token bucket. A bucket lets
// guessing continue for ever at a slower rate, which against a weak password is
// only a question of patience; a growing lockout makes the attempt count itself
// expensive. Legitimate users hit it rarely and recover by waiting.
//
// Keyed by email, not by IP. The threat is guessing one account's password, and
// an IP behind a proxy is whatever X-Forwarded-For says unless the proxy is
// trusted and parsed carefully — a key an attacker controls is worse than no
// key. The cost is that a spray across many accounts is not caught here.

import (
	"strings"
	"sync"
	"time"
)

const (
	// Failures tolerated before the account starts locking out. High enough
	// that fat-fingering a password a few times is never noticed.
	loginFreeAttempts = 5
	loginLockBase     = 30 * time.Second
	loginLockMax      = 15 * time.Minute
	// Failures are forgotten after this, so an account is not haunted by a
	// typo from last week.
	loginFailTTL = time.Hour
)

type loginFailure struct {
	count int
	last  time.Time
	until time.Time
}

type loginGuard struct {
	mu   sync.Mutex
	fail map[string]*loginFailure
}

func newLoginGuard() *loginGuard {
	return &loginGuard{fail: make(map[string]*loginFailure)}
}

func loginKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// locked reports whether this account is currently refusing attempts, and for
// how much longer.
func (g *loginGuard) locked(email string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneLocked()
	f := g.fail[loginKey(email)]
	if f == nil || time.Now().After(f.until) {
		return false, 0
	}
	return true, time.Until(f.until)
}

// fails records one bad attempt and extends the lockout.
func (g *loginGuard) fails(email string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneLocked()
	k := loginKey(email)
	f := g.fail[k]
	if f == nil {
		f = &loginFailure{}
		g.fail[k] = f
	}
	f.count++
	f.last = time.Now()
	if f.count > loginFreeAttempts {
		// Doubling, capped. Six failures buys 30s, ten buys eight minutes.
		wait := loginLockBase << uint(min(f.count-loginFreeAttempts-1, 8))
		if wait > loginLockMax {
			wait = loginLockMax
		}
		f.until = time.Now().Add(wait)
	}
}

// succeeds clears the record, so a correct password ends the lockout.
func (g *loginGuard) succeeds(email string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fail, loginKey(email))
}

// pruneLocked drops stale records. Without it the map is an unbounded table
// keyed by anything an anonymous caller cares to type into the email field.
func (g *loginGuard) pruneLocked() {
	cutoff := time.Now().Add(-loginFailTTL)
	for k, f := range g.fail {
		if f.last.Before(cutoff) && time.Now().After(f.until) {
			delete(g.fail, k)
		}
	}
}
