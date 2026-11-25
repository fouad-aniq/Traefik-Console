package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// loadAuthOptions reads the sign-in settings. COOKIE_SECURE defaults to true
// whenever the registered browser origin is https.
func loadAuthOptions(dbPath string) (AuthOptions, error) {
	out := AuthOptions{}
	path := getString("AUTH_DB_PATH", filepath.Join(filepath.Dir(dbPath), "auth.db"))
	abs, err := filepath.Abs(path)
	if err != nil {
		return out, fmt.Errorf("%w: AUTH_DB_PATH: %v", ErrInvalidConfigPath, err)
	}
	if abs == dbPath {
		return out, fmt.Errorf("AUTH_DB_PATH must differ from DB_PATH")
	}
	out.DBPath = abs

	switch raw := strings.ToLower(strings.TrimSpace(os.Getenv("COOKIE_SECURE"))); raw {
	case "":
		out.CookieSecure = strings.HasPrefix(strings.TrimSpace(os.Getenv("CONSOLE_ORIGIN")), "https://")
	case "true", "1", "yes":
		out.CookieSecure = true
	case "false", "0", "no":
		out.CookieSecure = false
	default:
		return out, fmt.Errorf("COOKIE_SECURE must be true or false")
	}

	idle, err := boundedInt("SESSION_IDLE_MINUTES", 120, 5, 7*24*60)
	if err != nil {
		return out, err
	}
	maxHours, err := boundedInt("SESSION_MAX_HOURS", 24, 1, 30*24)
	if err != nil {
		return out, err
	}
	out.IdleTimeout = time.Duration(idle) * time.Minute
	out.MaxLifetime = time.Duration(maxHours) * time.Hour

	for raw := range strings.SplitSeq(os.Getenv("TRUSTED_PROXIES"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			if ip := net.ParseIP(raw); ip != nil && ip.To4() != nil {
				raw += "/32"
			} else {
				raw += "/128"
			}
		}
		_, network, perr := net.ParseCIDR(raw)
		if perr != nil {
			return out, fmt.Errorf("invalid TRUSTED_PROXIES entry %q", raw)
		}
		out.TrustedProxies = append(out.TrustedProxies, network)
	}
	return out, nil
}

func boundedInt(key string, fallback, lo, hi int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s must be a whole number between %d and %d", key, lo, hi)
	}
	return n, nil
}
