package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	cookieName  = "tinyfm_session"
	maxUnlocked = 64
)

type session struct {
	Admin    string   `json:"a,omitempty"`
	Unlocked []string `json:"u,omitempty"`
	Exp      int64    `json:"exp"`
}

func adminFingerprint(secret []byte, password string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("admin\x00"))
	mac.Write([]byte(password))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func (s *session) isAdmin(secret []byte, adminPassword string) bool {
	if s == nil || s.Admin == "" {
		return false
	}
	return hmac.Equal([]byte(s.Admin), []byte(adminFingerprint(secret, adminPassword)))
}

func (s *session) holds(id string) bool {
	if s == nil || id == "" {
		return false
	}
	for _, v := range s.Unlocked {
		if v == id {
			return true
		}
	}
	return false
}

func (s *session) grant(id string) {
	if id == "" || s.holds(id) {
		return
	}
	s.Unlocked = append(s.Unlocked, id)
	if len(s.Unlocked) > maxUnlocked {
		s.Unlocked = s.Unlocked[len(s.Unlocked)-maxUnlocked:]
	}
}

func encodeSession(secret []byte, sess *session) string {
	payload, _ := json.Marshal(sess)
	p := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(p))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return p + "." + sig
}

func decodeSession(secret []byte, value string) *session {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" {
		return nil
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil
	}
	var s session
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil
	}
	if s.Exp <= time.Now().Unix() {
		return nil
	}
	return &s
}

func (s *server) loadSession(r *http.Request) *session {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return &session{}
	}
	sess := decodeSession(s.secretBytes(), c.Value)
	if sess == nil {
		return &session{}
	}
	return sess
}

func (s *server) saveSession(w http.ResponseWriter, sess *session, hours int, secure bool) {
	if sess.Exp == 0 {
		sess.Exp = time.Now().Add(time.Duration(hours) * time.Hour).Unix()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    encodeSession(s.secretBytes(), sess),
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   hours * 3600,
	})
}

func (s *server) clearSession(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// ---- rate limiting ----

const (
	limiterWindow     = 5 * time.Minute
	limiterMaxFail    = 5
	limiterBan        = 10 * time.Minute
	limiterMaxEntries = 10000
)

type attempt struct {
	fails       int
	windowStart time.Time
	bannedUntil time.Time
}

type limiter struct {
	mu sync.Mutex
	m  map[string]*attempt
}

func newLimiter() *limiter { return &limiter{m: make(map[string]*attempt)} }

// allow reports whether a request from key may proceed.
func (l *limiter) allow(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[key]
	if a == nil {
		return true, 0
	}
	if now.Before(a.bannedUntil) {
		return false, a.bannedUntil.Sub(now)
	}
	if now.Sub(a.windowStart) > limiterWindow {
		delete(l.m, key)
		return true, 0
	}
	return true, 0
}

// fail records a failed attempt and returns remaining tries (0 when banned).
func (l *limiter) fail(key string) (remaining int, banned time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[key]
	if a == nil || now.Sub(a.windowStart) > limiterWindow {
		a = &attempt{windowStart: now}
		l.m[key] = a
	}
	a.fails++
	if a.fails >= limiterMaxFail {
		a.bannedUntil = now.Add(limiterBan)
		a.fails = 0
		a.windowStart = now
		l.sweepLocked(now)
		return 0, limiterBan
	}
	l.sweepLocked(now)
	return limiterMaxFail - a.fails, 0
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}

func (l *limiter) sweepLocked(now time.Time) {
	if len(l.m) <= limiterMaxEntries {
		return
	}
	for k, a := range l.m {
		if now.After(a.bannedUntil) && now.Sub(a.windowStart) > limiterWindow {
			delete(l.m, k)
		}
	}
	if len(l.m) <= limiterMaxEntries {
		return
	}
	for k := range l.m {
		delete(l.m, k)
		if len(l.m) <= limiterMaxEntries/2 {
			break
		}
	}
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if ip := net.ParseIP(first); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
