package services

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/traefik-console/internal/models"
)

var serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9*]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// normalize validates a submitted service for its protocol and returns the definition to store.
func normalize(input models.ServiceConfig) (models.ServiceConfig, error) {
	input.UsedBy = nil // computed at read time, never stored
	protocol := models.ServiceProtocol(input)
	if protocol != models.ServiceHTTP && protocol != models.ServiceTCP && protocol != models.ServiceUDP {
		return input, ErrServiceInvalidProtocol
	}
	input.Protocol = protocol
	if len(input.Servers) == 0 {
		return input, ErrServiceNoServers
	}
	if len(input.Servers) > 64 {
		return input, fmt.Errorf("%w: at most 64 backend servers", ErrServiceInvalidOption)
	}
	if protocol == models.ServiceHTTP {
		return normalizeHTTP(input)
	}
	return normalizeTransport(input)
}

func normalizeHTTP(input models.ServiceConfig) (models.ServiceConfig, error) {
	if input.TCP != nil {
		return input, fmt.Errorf("%w: tcp options on an http service", ErrServiceInvalidOption)
	}
	for i, srv := range input.Servers {
		if srv.Address != "" {
			return input, fmt.Errorf("%w: server[%d] has an address; http servers use a URL", ErrServiceInvalidURL, i)
		}
		u, err := url.Parse(strings.TrimSpace(srv.URL))
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "h2c") {
			return input, fmt.Errorf("%w: server[%d] '%s'", ErrServiceInvalidURL, i, srv.URL)
		}
		if p := u.Port(); p != "" && !validPort(p) {
			return input, fmt.Errorf("%w: server[%d] '%s'", ErrServiceInvalidURL, i, srv.URL)
		}
		input.Servers[i] = models.ServerEndpoint{URL: u.String()}
	}
	if h := input.HealthCheck; h != nil {
		if h.Path == "" {
			h.Path = "/"
		}
		if !strings.HasPrefix(h.Path, "/") || len(h.Path) > 2048 || strings.ContainsAny(h.Path, "\r\n\x00") {
			return input, ErrServiceInvalidURL
		}
		for _, raw := range []string{h.Interval, h.Timeout} {
			if !validDuration(raw) {
				return input, ErrServiceInvalidURL
			}
		}
	}
	return input, nil
}

// normalizeTransport validates TCP and UDP services: host:port backends, and for TCP its options.
func normalizeTransport(input models.ServiceConfig) (models.ServiceConfig, error) {
	protocol := input.Protocol
	if input.PassHostHeader != nil || input.HealthCheck != nil {
		return input, fmt.Errorf("%w: pass_host_header and health_check are http options", ErrServiceInvalidOption)
	}
	seen := map[string]bool{}
	for i, srv := range input.Servers {
		address := strings.TrimSpace(srv.Address)
		if srv.URL != "" || !validAddress(address) {
			return input, fmt.Errorf("%w: server[%d] '%s'", ErrServiceInvalidAddress, i, srv.Address)
		}
		if seen[address] {
			return input, fmt.Errorf("%w: server[%d] '%s' is listed twice", ErrServiceInvalidAddress, i, address)
		}
		seen[address] = true
		input.Servers[i] = models.ServerEndpoint{Address: address}
	}
	if protocol == models.ServiceUDP {
		if input.TCP != nil || input.InsecureSkipVerify {
			return input, fmt.Errorf("%w: udp services have backend servers only", ErrServiceInvalidOption)
		}
		return input, nil
	}

	o := input.TCP
	if o == nil {
		if input.InsecureSkipVerify {
			return input, fmt.Errorf("%w: skipping verification needs TLS to the backends", ErrServiceInvalidOption)
		}
		return input, nil
	}
	o.ServerName = strings.TrimSpace(o.ServerName)
	if !o.TLS && (o.ServerName != "" || input.InsecureSkipVerify) {
		return input, fmt.Errorf("%w: server name and verification apply to TLS backends only", ErrServiceInvalidOption)
	}
	if o.ServerName != "" && !serverNamePattern.MatchString(o.ServerName) {
		return input, fmt.Errorf("%w: server name '%s'", ErrServiceInvalidOption, o.ServerName)
	}
	if o.ProxyProtocol < 0 || o.ProxyProtocol > 2 {
		return input, fmt.Errorf("%w: PROXY protocol version must be 1 or 2", ErrServiceInvalidOption)
	}
	if !validDuration(o.DialTimeout) || !validDuration(o.TerminationDelay) {
		return input, fmt.Errorf("%w: durations must be positive and at most 1h", ErrServiceInvalidOption)
	}
	if h := o.HealthCheck; h != nil {
		if !validDuration(h.Interval) || !validDuration(h.Timeout) {
			return input, fmt.Errorf("%w: health check durations must be positive and at most 1h", ErrServiceInvalidOption)
		}
		if h.Port < 0 || h.Port > 65535 {
			return input, fmt.Errorf("%w: health check port", ErrServiceInvalidOption)
		}
		for _, payload := range []string{h.Send, h.Expect} {
			if len(payload) > 1024 || strings.ContainsRune(payload, 0) {
				return input, fmt.Errorf("%w: health check send/expect", ErrServiceInvalidOption)
			}
		}
		if h.Expect != "" && h.Send == "" {
			return input, fmt.Errorf("%w: expect needs a payload to send", ErrServiceInvalidOption)
		}
	}
	if *o == (models.TCPServiceOptions{}) {
		input.TCP = nil
	}
	return input, nil
}

// validAddress accepts host:port (IPv6 in brackets) with a port between 1 and 65535.
func validAddress(address string) bool {
	if address == "" || len(address) > 261 || strings.ContainsAny(address, " \t\r\n/?#@") || strings.Contains(address, "://") {
		return false
	}
	host, port, err := net.SplitHostPort(address)
	return err == nil && host != "" && validPort(port)
}

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == port
}

// validDuration is an empty value (Traefik default) or a Go duration in (0, 1h].
func validDuration(raw string) bool {
	if raw == "" {
		return true
	}
	d, err := time.ParseDuration(raw)
	return err == nil && d > 0 && d <= time.Hour
}
