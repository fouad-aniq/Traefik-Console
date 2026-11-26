package config

import "testing"

func TestConsoleUpstreamDefaultsToTheContainerAndPort(t *testing.T) {
	resetDockerEnv(t)
	t.Setenv("PORT", "8081")
	t.Setenv("BIND_ADDRESS", "127.0.0.1")
	t.Setenv("CONSOLE_UPSTREAM", "")
	opts, err := Load()
	if err != nil || opts.ConsoleUpstream != "http://console:8081" {
		t.Fatalf("default: %v %q", err, opts.ConsoleUpstream)
	}
	t.Setenv("CONSOLE_UPSTREAM", "http://traefik-console:9000/")
	if opts, err = Load(); err != nil || opts.ConsoleUpstream != "http://traefik-console:9000" {
		t.Fatalf("set: %v %+v", err, opts)
	}
	for _, bad := range []string{"console:8081", "ftp://console:21", "http://user@console:8081", "http://console:8081/path"} {
		t.Setenv("CONSOLE_UPSTREAM", bad)
		if _, err = Load(); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}
