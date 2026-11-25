package docker

import (
	"net"
	"regexp"
	"strings"
	"time"
)

func ValidateSecretName(name string) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`).MatchString(name) {
		return ErrInvalidRequest
	}
	return nil
}

func ValidateDecisionInput(ip, duration, reason string, adding bool) error {
	if net.ParseIP(ip) == nil {
		if _, _, e := net.ParseCIDR(ip); e != nil {
			return ErrInvalidRequest
		}
	}

	if !adding {
		if duration != "" || reason != "" {
			return ErrInvalidRequest
		}
		return nil
	}

	d, e := time.ParseDuration(duration)
	if e != nil || d <= 0 || d > 7*24*time.Hour || len(reason) > 256 || strings.ContainsAny(reason, "\x00\r\n") {
		return ErrInvalidRequest
	}

	return nil
}

func ValidateBouncerInput(id int64, key string, registering bool) error {
	if id <= 0 {
		return ErrInvalidRequest
	}

	if !registering {
		if key != "" {
			return ErrInvalidRequest
		}
		return nil
	}

	if len(key) != 64 {
		return ErrInvalidRequest
	}
	for _, c := range key {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return ErrInvalidRequest
		}
	}

	return nil
}
