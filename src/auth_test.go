package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newRequestWithRemote(remote string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	return r
}

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func TestSessionRoundTrip(t *testing.T) {
	s := &session{
		Admin:    adminFingerprint(testSecret, "adminpw"),
		Unlocked: []string{"abc", "def"},
		Exp:      time.Now().Add(time.Hour).Unix(),
	}
	value := encodeSession(testSecret, s)
	got := decodeSession(testSecret, value)
	if got == nil {
		t.Fatal("decode failed")
	}
	if got.Admin != s.Admin || len(got.Unlocked) != 2 || !got.holds("abc") || got.holds("zzz") {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestSessionTamper(t *testing.T) {
	s := &session{Unlocked: []string{"abc"}, Exp: time.Now().Add(time.Hour).Unix()}
	value := encodeSession(testSecret, s)
	if decodeSession(testSecret, value+"x") != nil {
		t.Fatal("tampered signature accepted")
	}
	if decodeSession([]byte("another-secret-0000000000000000"), value) != nil {
		t.Fatal("wrong secret accepted")
	}
	if decodeSession(testSecret, "garbage") != nil {
		t.Fatal("garbage accepted")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := &session{Exp: time.Now().Add(-time.Minute).Unix()}
	if decodeSession(testSecret, encodeSession(testSecret, s)) != nil {
		t.Fatal("expired session accepted")
	}
}

func TestSessionAdminFingerprint(t *testing.T) {
	s := &session{Admin: adminFingerprint(testSecret, "old")}
	if !s.isAdmin(testSecret, "old") {
		t.Fatal("admin fingerprint rejected")
	}
	if s.isAdmin(testSecret, "new") {
		t.Fatal("stale admin fingerprint accepted")
	}
	if s.isAdmin(testSecret, "") {
		t.Fatal("session with a password fingerprint must not be admin when the config password is empty")
	}
	empty := &session{}
	if empty.isAdmin(testSecret, "") {
		t.Fatal("empty session must not be admin")
	}
	noPassword := &session{Admin: adminFingerprint(testSecret, "")}
	if !noPassword.isAdmin(testSecret, "") {
		t.Fatal("empty admin password should authenticate its fingerprint")
	}
	if noPassword.isAdmin(testSecret, "old") {
		t.Fatal("empty-password session must not authenticate after a password is set")
	}
}

func TestSessionGrant(t *testing.T) {
	s := &session{}
	s.grant("a")
	s.grant("a")
	s.grant("b")
	if len(s.Unlocked) != 2 {
		t.Fatalf("grants = %v", s.Unlocked)
	}
	for i := 0; i < maxUnlocked+10; i++ {
		s.grant(string(rune('A'+i%26)) + string(rune('a'+i/26)))
	}
	if len(s.Unlocked) > maxUnlocked {
		t.Fatalf("unlocked list not capped: %d", len(s.Unlocked))
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter()
	key := "192.0.2.1"
	for i := 0; i < limiterMaxFail-1; i++ {
		if ok, _ := l.allow(key); !ok {
			t.Fatalf("blocked too early at %d", i)
		}
		if remaining, banned := l.fail(key); banned != 0 || remaining != limiterMaxFail-i-1 {
			t.Fatalf("fail %d: remaining=%d banned=%v", i, remaining, banned)
		}
	}
	if _, banned := l.fail(key); banned == 0 {
		t.Fatal("no ban after max failures")
	}
	if ok, wait := l.allow(key); ok || wait <= 0 {
		t.Fatalf("allow after ban = %v, wait = %v", ok, wait)
	}
	l.reset(key)
	if ok, _ := l.allow(key); !ok {
		t.Fatal("reset did not clear ban")
	}
}

func TestClientIP(t *testing.T) {
	r := newRequestWithRemote("203.0.113.9:1234")
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	if got := clientIP(r, false); got != "203.0.113.9" {
		t.Fatalf("without trust = %q", got)
	}
	if got := clientIP(r, true); got != "198.51.100.7" {
		t.Fatalf("with trust = %q", got)
	}
}
