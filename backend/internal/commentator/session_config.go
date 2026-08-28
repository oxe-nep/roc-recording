package commentator

import (
	"os"
	"strings"
	"time"
)

const defaultSessionTTL = 24 * time.Hour

// SessionTTLFromEnv reads COMMENTATOR_SESSION_TTL (Go duration, e.g. "24h", "48h").
func SessionTTLFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("COMMENTATOR_SESSION_TTL"))
	if raw == "" {
		return defaultSessionTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultSessionTTL
	}
	return d
}
