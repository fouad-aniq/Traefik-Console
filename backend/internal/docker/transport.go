package docker

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// endpointTransport accepts only a fixed operator endpoint, never a request URL.
// No ambient HTTP proxy, redirects, userinfo, query or Docker path prefix.
func endpointTransport(endpoint string) (string, *http.Transport, error) {
	invalid := func() (string, *http.Transport, error) {
		return "", nil, fmt.Errorf("DOCKER_HOST must be unix:///absolute/socket or an http(s)://host[:port] or tcp://host:port endpoint")
	}

	u, e := url.Parse(endpoint)
	if e != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return invalid()
	}

	transport := &http.Transport{Proxy: nil, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, DisableCompression: true, ResponseHeaderTimeout: 10 * time.Second, TLSHandshakeTimeout: 5 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	switch u.Scheme {
	case "unix":
		if u.Host != "" || !filepath.IsAbs(u.Path) || filepath.Clean(u.Path) != u.Path || u.Path == "/" {
			return invalid()
		}

		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", u.Path)
		}

		return "http://docker", transport, nil
	case "tcp", "http", "https":
		if u.Host == "" || u.Hostname() == "" || u.Path != "" && u.Path != "/" || strings.ContainsAny(u.Host, "\\\r\n") {
			return invalid()
		}

		if strings.Count(u.Host, ":") > 1 && !strings.HasPrefix(u.Host, "[") {
			return invalid()
		}

		if u.Scheme == "tcp" && u.Port() == "" {
			return invalid()
		}

		if port := u.Port(); port != "" {
			n, e := strconv.Atoi(port)
			if e != nil || n < 1 || n > 65535 {
				return invalid()
			}
		}

		if strings.HasSuffix(u.Host, ":") {
			return invalid()
		}

		if u.Scheme == "tcp" {
			u.Scheme = "http"
		}
		u.Path = ""
		transport.DialContext = dialer.DialContext
		return u.String(), transport, nil
	default:
		return invalid()
	}
}
