package commentator

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const joinRateLimit = 30 // max join/ws attempts per IP per minute

type joinLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newJoinLimiter() *joinLimiter {
	return &joinLimiter{hits: make(map[string][]time.Time)}
}

func (l *joinLimiter) allow(ip string) bool {
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := l.hits[ip]
	kept := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= joinRateLimit {
		l.hits[ip] = kept
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
