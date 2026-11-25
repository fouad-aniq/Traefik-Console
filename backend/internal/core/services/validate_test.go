package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/traefik-console/internal/models"
)

// These tests drive normalize directly: every rule of every protocol, with the value on each side of
// each boundary. Nothing here touches the store or the engine.

func httpWith(edit func(*models.ServiceConfig), urls ...string) models.ServiceConfig {
	cfg := servers(urls...)
	if edit != nil {
		edit(&cfg)
	}
	return cfg
}

func tcpWith(edit func(*models.ServiceConfig), addrs ...string) models.ServiceConfig {
	cfg := tcpService(addrs...)
	if edit != nil {
		edit(&cfg)
	}
	return cfg
}

func tcpOpts(o models.TCPServiceOptions, tweak ...func(*models.ServiceConfig)) models.ServiceConfig {
	return tcpWith(func(c *models.ServiceConfig) {
		c.TCP = &o
		for _, t := range tweak {
			t(c)
		}
	}, "db:5432")
}

func manyServers(protocol string, n int) models.ServiceConfig {
	cfg := models.ServiceConfig{Protocol: protocol}
	for i := 0; i < n; i++ {
		if protocol == "http" {
			cfg.Servers = append(cfg.Servers, models.ServerEndpoint{URL: fmt.Sprintf("http://host%d:80", i)})
		} else {
			cfg.Servers = append(cfg.Servers, models.ServerEndpoint{Address: fmt.Sprintf("host%d:80", i)})
		}
	}
	return cfg
}

func TestNormalizeAcceptsAndCleansHTTPServices(t *testing.T) {
	long := "/" + strings.Repeat("a", 2047)
	for name, c := range map[string]struct {
		in   models.ServiceConfig
		urls []string
	}{
		"plain http":            {servers("http://a"), []string{"http://a"}},
		"https with port, path": {servers("https://a:8443/base"), []string{"https://a:8443/base"}},
		"h2c":                   {servers("h2c://grpc:50051"), []string{"h2c://grpc:50051"}},
		"ipv6":                  {servers("http://[::1]:8080"), []string{"http://[::1]:8080"}},
		"lowest port":           {servers("http://a:1"), []string{"http://a:1"}},
		"highest port":          {servers("http://a:65535"), []string{"http://a:65535"}},
		"spaces around":         {servers("  http://a:80  "), []string{"http://a:80"}},
		"trailing slash kept":   {servers("http://a/"), []string{"http://a/"}},
		"several servers":       {servers("http://a", "https://b", "h2c://c"), []string{"http://a", "https://b", "h2c://c"}},
		"scheme is lowercased":  {servers("HTTP://a"), []string{"http://a"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := normalize(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Protocol != "http" || len(got.Servers) != len(c.urls) {
				t.Fatalf("%+v", got)
			}
			for i, u := range c.urls {
				if got.Servers[i].URL != u || got.Servers[i].Address != "" {
					t.Fatalf("server %d: %+v", i, got.Servers[i])
				}
			}
		})
	}
	t.Run("an empty protocol is http", func(t *testing.T) {
		got, err := normalize(models.ServiceConfig{Servers: servers("http://a").Servers})
		if err != nil || got.Protocol != "http" {
			t.Fatalf("%+v %v", got, err)
		}
	})
	t.Run("up to 64 servers", func(t *testing.T) {
		if _, err := normalize(manyServers("http", 64)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("pass host header and skipping verification are http options", func(t *testing.T) {
		yes, no := true, false
		for _, v := range []*bool{&yes, &no} {
			cfg := servers("https://a")
			cfg.PassHostHeader = v
			cfg.InsecureSkipVerify = true
			got, err := normalize(cfg)
			if err != nil || got.PassHostHeader == nil || *got.PassHostHeader != *v || !got.InsecureSkipVerify {
				t.Fatalf("%+v %v", got, err)
			}
		}
	})
	t.Run("a health check path of the longest accepted length", func(t *testing.T) {
		cfg := httpWith(func(c *models.ServiceConfig) { c.HealthCheck = &models.ServiceHealth{Path: long} }, "http://a")
		if _, err := normalize(cfg); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("health check durations", func(t *testing.T) {
		for _, d := range []string{"", "1ms", "500ms", "1s", "30s", "1m30s", "59m", "1h"} {
			cfg := httpWith(func(c *models.ServiceConfig) {
				c.HealthCheck = &models.ServiceHealth{Path: "/", Interval: d, Timeout: d}
			}, "http://a")
			if _, err := normalize(cfg); err != nil {
				t.Fatalf("%q: %v", d, err)
			}
		}
	})
	t.Run("usage a client sends is dropped", func(t *testing.T) {
		cfg := servers("http://a")
		cfg.UsedBy = &models.ServiceUsage{Routers: []models.RouterRef{{Name: "forged"}}}
		if got, err := normalize(cfg); err != nil || got.UsedBy != nil {
			t.Fatalf("%+v %v", got, err)
		}
	})
}

func TestNormalizeRefusesInvalidHTTPServices(t *testing.T) {
	health := func(h models.ServiceHealth) models.ServiceConfig {
		return httpWith(func(c *models.ServiceConfig) { c.HealthCheck = &h }, "http://a")
	}
	cases := map[string]struct {
		cfg  models.ServiceConfig
		want error
	}{
		"no servers":                   {servers(), ErrServiceNoServers},
		"65 servers":                   {manyServers("http", 65), ErrServiceInvalidOption},
		"empty url":                    {servers(""), ErrServiceInvalidURL},
		"blank url":                    {servers("   "), ErrServiceInvalidURL},
		"host only":                    {servers("app:8080"), ErrServiceInvalidURL},
		"no scheme":                    {servers("//app"), ErrServiceInvalidURL},
		"no host":                      {servers("http://"), ErrServiceInvalidURL},
		"tcp scheme":                   {servers("tcp://a:1"), ErrServiceInvalidURL},
		"udp scheme":                   {servers("udp://a:1"), ErrServiceInvalidURL},
		"ws scheme":                    {servers("ws://a"), ErrServiceInvalidURL},
		"ftp scheme":                   {servers("ftp://a"), ErrServiceInvalidURL},
		"file scheme":                  {servers("file:///etc/passwd"), ErrServiceInvalidURL},
		"port zero":                    {servers("http://a:0"), ErrServiceInvalidURL},
		"port too big":                 {servers("http://a:65536"), ErrServiceInvalidURL},
		"port far too big":             {servers("http://a:700000"), ErrServiceInvalidURL},
		"port with leading zero":       {servers("http://a:080"), ErrServiceInvalidURL},
		"credentials":                  {servers("http://user:pass@a"), ErrServiceInvalidURL},
		"user only":                    {servers("http://user@a"), ErrServiceInvalidURL},
		"query":                        {servers("http://a/?x=1"), ErrServiceInvalidURL},
		"fragment":                     {servers("http://a/#x"), ErrServiceInvalidURL},
		"malformed":                    {servers("http://a b"), ErrServiceInvalidURL},
		"second server is bad":         {servers("http://a", "nope"), ErrServiceInvalidURL},
		"last server is bad":           {servers("http://a", "http://b", "http://"), ErrServiceInvalidURL},
		"an address instead of a url":  {httpWith(func(c *models.ServiceConfig) { c.Servers = []models.ServerEndpoint{{Address: "a:1"}} }), ErrServiceInvalidURL},
		"url and address together":     {httpWith(func(c *models.ServiceConfig) { c.Servers = []models.ServerEndpoint{{URL: "http://a", Address: "a:1"}} }), ErrServiceInvalidURL},
		"tcp options":                  {httpWith(func(c *models.ServiceConfig) { c.TCP = &models.TCPServiceOptions{} }, "http://a"), ErrServiceInvalidOption},
		"health path is relative":      {health(models.ServiceHealth{Path: "health"}), ErrServiceInvalidURL},
		"health path with a newline":   {health(models.ServiceHealth{Path: "/a\nb"}), ErrServiceInvalidURL},
		"health path with a return":    {health(models.ServiceHealth{Path: "/a\rb"}), ErrServiceInvalidURL},
		"health path with NUL":         {health(models.ServiceHealth{Path: "/a\x00"}), ErrServiceInvalidURL},
		"health path too long":         {health(models.ServiceHealth{Path: "/" + strings.Repeat("a", 2048)}), ErrServiceInvalidURL},
		"health interval not a time":   {health(models.ServiceHealth{Path: "/", Interval: "soon"}), ErrServiceInvalidURL},
		"health interval zero":         {health(models.ServiceHealth{Path: "/", Interval: "0s"}), ErrServiceInvalidURL},
		"health interval negative":     {health(models.ServiceHealth{Path: "/", Interval: "-1s"}), ErrServiceInvalidURL},
		"health interval over an hour": {health(models.ServiceHealth{Path: "/", Interval: "1h1s"}), ErrServiceInvalidURL},
		"health timeout not a time":    {health(models.ServiceHealth{Path: "/", Timeout: "10"}), ErrServiceInvalidURL},
		"health timeout two hours":     {health(models.ServiceHealth{Path: "/", Timeout: "2h"}), ErrServiceInvalidURL},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := normalize(c.cfg); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestNormalizeHTTPHealthCheckPathDefaultsToRoot(t *testing.T) {
	got, err := normalize(httpWith(func(c *models.ServiceConfig) { c.HealthCheck = &models.ServiceHealth{Interval: "10s"} }, "http://a"))
	if err != nil || got.HealthCheck.Path != "/" || got.HealthCheck.Interval != "10s" {
		t.Fatalf("%+v %v", got.HealthCheck, err)
	}
}

func TestNormalizeAcceptsAndCleansTCPAndUDPServices(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		for name, c := range map[string]struct {
			in   []string
			want []string
		}{
			"host and port":             {[]string{"db:5432"}, []string{"db:5432"}},
			"ip":                        {[]string{"10.0.0.5:1"}, []string{"10.0.0.5:1"}},
			"highest port":              {[]string{"db:65535"}, []string{"db:65535"}},
			"ipv6 in brackets":          {[]string{"[::1]:5432"}, []string{"[::1]:5432"}},
			"ipv6 with a zone-less ula": {[]string{"[fd00::5]:53"}, []string{"[fd00::5]:53"}},
			"a fully qualified name":    {[]string{"db.internal.example.com:8080"}, []string{"db.internal.example.com:8080"}},
			"spaces around":             {[]string{"  db:5432 "}, []string{"db:5432"}},
			"a trailing newline":        {[]string{"db:5432\n"}, []string{"db:5432"}},
			"same host, other ports":    {[]string{"db:5432", "db:5433"}, []string{"db:5432", "db:5433"}},
			"several backends":          {[]string{"a:1", "b:2", "c:3"}, []string{"a:1", "b:2", "c:3"}},
		} {
			t.Run(protocol+" "+name, func(t *testing.T) {
				cfg := tcpService(c.in...)
				cfg.Protocol = protocol
				got, err := normalize(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if got.Protocol != protocol || len(got.Servers) != len(c.want) {
					t.Fatalf("%+v", got)
				}
				for i, a := range c.want {
					if got.Servers[i].Address != a || got.Servers[i].URL != "" {
						t.Fatalf("server %d: %+v", i, got.Servers[i])
					}
				}
			})
		}
		t.Run(protocol+" up to 64 servers", func(t *testing.T) {
			if _, err := normalize(manyServers(protocol, 64)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNormalizeRefusesInvalidAddresses(t *testing.T) {
	bad := map[string]string{
		"empty":                  "",
		"blank":                  "   ",
		"no port":                "db",
		"empty port":             "db:",
		"no host":                ":5432",
		"port zero":              "db:0",
		"port too big":           "db:65536",
		"negative port":          "db:-1",
		"port with letters":      "db:54a",
		"port with leading zero": "db:05432",
		"port with a plus":       "db:+5432",
		"a url":                  "tcp://db:5432",
		"an http url":            "http://db:5432",
		"a path":                 "db:5432/x",
		"a query":                "db:5432?x=1",
		"a fragment":             "db:5432#x",
		"credentials":            "user@db:5432",
		"a space in the host":    "my db:5432",
		"a tab":                  "db\t:5432",
		"a newline inside":       "db\n:5432",
		"ipv6 without brackets":  "::1:5432",
		"ipv6 without a port":    "[::1]",
		"too long":               strings.Repeat("a", 260) + ":1",
	}
	for _, protocol := range []string{"tcp", "udp"} {
		for name, address := range bad {
			t.Run(protocol+" "+name, func(t *testing.T) {
				cfg := models.ServiceConfig{Protocol: protocol, Servers: []models.ServerEndpoint{{Address: address}}}
				if _, err := normalize(cfg); !errors.Is(err, ErrServiceInvalidAddress) {
					t.Fatalf("%q: got %v", address, err)
				}
			})
		}
		t.Run(protocol+" no servers", func(t *testing.T) {
			if _, err := normalize(models.ServiceConfig{Protocol: protocol}); !errors.Is(err, ErrServiceNoServers) {
				t.Fatal(err)
			}
		})
		t.Run(protocol+" 65 servers", func(t *testing.T) {
			if _, err := normalize(manyServers(protocol, 65)); !errors.Is(err, ErrServiceInvalidOption) {
				t.Fatal(err)
			}
		})
		t.Run(protocol+" a url instead of an address", func(t *testing.T) {
			cfg := models.ServiceConfig{Protocol: protocol, Servers: []models.ServerEndpoint{{URL: "http://db:5432"}}}
			if _, err := normalize(cfg); !errors.Is(err, ErrServiceInvalidAddress) {
				t.Fatal(err)
			}
		})
		t.Run(protocol+" a url next to an address", func(t *testing.T) {
			cfg := models.ServiceConfig{Protocol: protocol, Servers: []models.ServerEndpoint{{Address: "db:5432", URL: "http://db"}}}
			if _, err := normalize(cfg); !errors.Is(err, ErrServiceInvalidAddress) {
				t.Fatal(err)
			}
		})
		t.Run(protocol+" the same backend twice", func(t *testing.T) {
			cfg := models.ServiceConfig{Protocol: protocol, Servers: []models.ServerEndpoint{{Address: "db:5432"}, {Address: "other:1"}, {Address: " db:5432 "}}}
			if _, err := normalize(cfg); !errors.Is(err, ErrServiceInvalidAddress) {
				t.Fatal(err)
			}
		})
		t.Run(protocol+" the second server is bad", func(t *testing.T) {
			if _, err := normalize(models.ServiceConfig{Protocol: protocol, Servers: []models.ServerEndpoint{{Address: "a:1"}, {Address: "b"}}}); !errors.Is(err, ErrServiceInvalidAddress) {
				t.Fatal(err)
			}
		})
		t.Run(protocol+" http options", func(t *testing.T) {
			yes := true
			for what, edit := range map[string]func(*models.ServiceConfig){
				"pass host header": func(c *models.ServiceConfig) { c.PassHostHeader = &yes },
				"health check":     func(c *models.ServiceConfig) { c.HealthCheck = &models.ServiceHealth{Path: "/"} },
			} {
				cfg := models.ServiceConfig{Protocol: protocol, Servers: []models.ServerEndpoint{{Address: "db:5432"}}}
				edit(&cfg)
				if _, err := normalize(cfg); !errors.Is(err, ErrServiceInvalidOption) {
					t.Fatalf("%s: %v", what, err)
				}
			}
		})
	}
}

func TestNormalizeProtocolRules(t *testing.T) {
	for _, p := range []string{"TCP", "Http", "sctp", "tls", "ws", " tcp", "udp "} {
		t.Run(p, func(t *testing.T) {
			if _, err := normalize(models.ServiceConfig{Protocol: p, Servers: servers("http://a").Servers}); !errors.Is(err, ErrServiceInvalidProtocol) {
				t.Fatalf("%q: %v", p, err)
			}
		})
	}
	t.Run("usage is dropped for tcp and udp too", func(t *testing.T) {
		for _, p := range []string{"tcp", "udp"} {
			cfg := tcpService("db:5432")
			cfg.Protocol = p
			cfg.UsedBy = &models.ServiceUsage{Routers: []models.RouterRef{{Name: "forged"}}}
			if got, err := normalize(cfg); err != nil || got.UsedBy != nil {
				t.Fatalf("%s: %+v %v", p, got, err)
			}
		}
	})
}

func TestNormalizeTCPOptions(t *testing.T) {
	skip := func(c *models.ServiceConfig) { c.InsecureSkipVerify = true }
	name253 := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)

	valid := map[string]models.ServiceConfig{
		"tls alone":                      tcpOpts(models.TCPServiceOptions{TLS: true}),
		"tls with a server name":         tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "db.example.com"}),
		"a wildcard server name":         tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "*.example.com"}),
		"a one-letter server name":       tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "a"}),
		"the longest server name":        tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: name253}),
		"a server name with spaces":      tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "  db.example.com  "}),
		"tls with verification skipped":  tcpOpts(models.TCPServiceOptions{TLS: true}, skip),
		"proxy protocol 1":               tcpOpts(models.TCPServiceOptions{ProxyProtocol: 1}),
		"proxy protocol 2":               tcpOpts(models.TCPServiceOptions{ProxyProtocol: 2}),
		"proxy protocol off":             tcpOpts(models.TCPServiceOptions{ProxyProtocol: 0, DialTimeout: "1s"}),
		"dial timeout":                   tcpOpts(models.TCPServiceOptions{DialTimeout: "5s"}),
		"the longest dial timeout":       tcpOpts(models.TCPServiceOptions{DialTimeout: "1h"}),
		"a short termination delay":      tcpOpts(models.TCPServiceOptions{TerminationDelay: "1ms"}),
		"health check, defaults":         tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{}}),
		"health check, every field":      tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Interval: "10s", Timeout: "2s", Port: 9000, Send: "PING", Expect: "PONG"}}),
		"health check, lowest port":      tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Port: 1}}),
		"health check, highest port":     tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Port: 65535}}),
		"health check, send only":        tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Send: "PING"}}),
		"health check, longest payloads": tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Send: strings.Repeat("s", 1024), Expect: strings.Repeat("e", 1024)}}),
	}
	for name, cfg := range valid {
		t.Run("valid: "+name, func(t *testing.T) {
			if _, err := normalize(cfg); err != nil {
				t.Fatal(err)
			}
		})
	}

	invalid := map[string]models.ServiceConfig{
		"a server name without tls":          tcpOpts(models.TCPServiceOptions{ServerName: "db.example.com"}),
		"skipping verification without tls":  tcpOpts(models.TCPServiceOptions{ProxyProtocol: 1}, skip),
		"a server name starting with a dash": tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "-db"}),
		"a server name ending with a dash":   tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "db-"}),
		"a server name with a space":         tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "my db"}),
		"a server name with an underscore":   tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "my_db"}),
		"a server name with a slash":         tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "db/x"}),
		"a server name with a port":          tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "db:5432"}),
		"a server name that is too long":     tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: name253 + "e"}),
		"proxy protocol 3":                   tcpOpts(models.TCPServiceOptions{ProxyProtocol: 3}),
		"proxy protocol -1":                  tcpOpts(models.TCPServiceOptions{ProxyProtocol: -1}),
		"dial timeout not a time":            tcpOpts(models.TCPServiceOptions{DialTimeout: "soon"}),
		"dial timeout zero":                  tcpOpts(models.TCPServiceOptions{DialTimeout: "0s"}),
		"dial timeout negative":              tcpOpts(models.TCPServiceOptions{DialTimeout: "-5s"}),
		"dial timeout over an hour":          tcpOpts(models.TCPServiceOptions{DialTimeout: "61m"}),
		"termination delay not a time":       tcpOpts(models.TCPServiceOptions{TerminationDelay: "5"}),
		"termination delay over an hour":     tcpOpts(models.TCPServiceOptions{TerminationDelay: "2h"}),
		"health interval not a time":         tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Interval: "x"}}),
		"health interval zero":               tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Interval: "0s"}}),
		"health timeout over an hour":        tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Timeout: "1h1m"}}),
		"health port too big":                tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Port: 65536}}),
		"health port negative":               tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Port: -1}}),
		"expect without send":                tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Expect: "PONG"}}),
		"send too long":                      tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Send: strings.Repeat("s", 1025)}}),
		"expect too long":                    tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Send: "x", Expect: strings.Repeat("e", 1025)}}),
		"send with a NUL":                    tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Send: "a\x00b"}}),
		"expect with a NUL":                  tcpOpts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Send: "a", Expect: "b\x00"}}),
	}
	for name, cfg := range invalid {
		t.Run("invalid: "+name, func(t *testing.T) {
			if _, err := normalize(cfg); !errors.Is(err, ErrServiceInvalidOption) {
				t.Fatalf("got %v", err)
			}
		})
	}
	t.Run("skipping verification needs the tcp options block to turn tls on", func(t *testing.T) {
		cfg := tcpService("db:5432")
		cfg.InsecureSkipVerify = true
		if _, err := normalize(cfg); !errors.Is(err, ErrServiceInvalidOption) {
			t.Fatal(err)
		}
	})
	t.Run("options that are all empty are not kept", func(t *testing.T) {
		got, err := normalize(tcpOpts(models.TCPServiceOptions{}))
		if err != nil || got.TCP != nil {
			t.Fatalf("%+v %v", got.TCP, err)
		}
	})
	t.Run("the server name is trimmed", func(t *testing.T) {
		got, err := normalize(tcpOpts(models.TCPServiceOptions{TLS: true, ServerName: "  db.example.com "}))
		if err != nil || got.TCP.ServerName != "db.example.com" {
			t.Fatalf("%+v %v", got.TCP, err)
		}
	})
	t.Run("a set option keeps the block", func(t *testing.T) {
		got, err := normalize(tcpOpts(models.TCPServiceOptions{ProxyProtocol: 2}))
		if err != nil || got.TCP == nil || got.TCP.ProxyProtocol != 2 {
			t.Fatalf("%+v %v", got.TCP, err)
		}
	})
}

func TestNormalizeUDPHasBackendsOnly(t *testing.T) {
	udp := func(edit func(*models.ServiceConfig)) models.ServiceConfig {
		cfg := tcpService("dns:53")
		cfg.Protocol = "udp"
		edit(&cfg)
		return cfg
	}
	for name, edit := range map[string]func(*models.ServiceConfig){
		"tcp options":           func(c *models.ServiceConfig) { c.TCP = &models.TCPServiceOptions{ProxyProtocol: 1} },
		"empty tcp options":     func(c *models.ServiceConfig) { c.TCP = &models.TCPServiceOptions{} },
		"skipping verification": func(c *models.ServiceConfig) { c.InsecureSkipVerify = true },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalize(udp(edit)); !errors.Is(err, ErrServiceInvalidOption) {
				t.Fatal(err)
			}
		})
	}
	t.Run("backends only is fine", func(t *testing.T) {
		got, err := normalize(udp(func(*models.ServiceConfig) {}))
		if err != nil || got.TCP != nil || got.Protocol != "udp" {
			t.Fatalf("%+v %v", got, err)
		}
	})
}

func TestValidationHelpers(t *testing.T) {
	for port, want := range map[string]bool{"1": true, "80": true, "65535": true, "0": false, "65536": false, "-1": false, "": false, "08": false, "8 ": false, "http": false, "1e3": false} {
		if validPort(port) != want {
			t.Errorf("validPort(%q) = %v", port, !want)
		}
	}
	for d, want := range map[string]bool{"": true, "1ns": true, "1ms": true, "1s": true, "1h": true, "60m": true, "3600s": true, "0": false, "0s": false, "-1s": false, "3601s": false, "1h0m1s": false, "1d": false, "abc": false, " 1s": false} {
		if validDuration(d) != want {
			t.Errorf("validDuration(%q) = %v", d, !want)
		}
	}
	for a, want := range map[string]bool{"a:1": true, "[::1]:80": true, "a.b.c:65535": true, "": false, "a": false, "a:": false, ":1": false, "a:0": false, "x://a:1": false} {
		if validAddress(a) != want {
			t.Errorf("validAddress(%q) = %v", a, !want)
		}
	}
}
