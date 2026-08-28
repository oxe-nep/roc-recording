package commentator

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func deckClaimOrigin(configured string, r *http.Request) string {
	configured = strings.TrimRight(strings.TrimSpace(configured), "/")
	if configured != "" && !isLoopbackOrPrivateOrigin(configured) {
		return configured
	}
	if r == nil {
		return configured
	}
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return configured
	}
	origin := proto + "://" + host
	return strings.TrimRight(origin, "/")
}

func isLoopbackOrPrivateOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return true
	}
	host := u.Hostname()
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
