package docker

import "testing"

func TestParseAlertsLocatesColumnsByHeader(t *testing.T) {
	raw := []byte("id,scope,value,reason,country,as,decisions,created_at\n" +
		"12,Ip,203.0.113.9,crowdsecurity/ssh-bf,FR,AS1 Example,ban:1,2026-11-10T09:00:00Z\n" +
		"13,Ip,198.51.100.4,\"crowdsecurity/http-probing\",,,,2026-11-10T10:00:00Z\n")
	got, err := parseAlerts(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].ID != 12 || got[0].Country != "FR" || got[0].Reason != "crowdsecurity/ssh-bf" || got[0].CreatedAt != "2026-11-10T09:00:00Z" {
		t.Fatalf("%+v", got[0])
	}
	if got[1].Country != "" || got[1].Value != "198.51.100.4" {
		t.Fatalf("%+v", got[1])
	}
	if empty, err := parseAlerts([]byte("")); err != nil || len(empty) != 0 {
		t.Fatal("empty output is an empty list")
	}
	if _, err := parseAlerts([]byte("not,a,table\n1,2,3")); err == nil {
		t.Fatal("a table without an id column must be rejected")
	}
}

func TestParseBouncers(t *testing.T) {
	got, err := parseBouncers([]byte(`[{"name":"traefik-console-op-1","ip_address":"172.18.0.4","type":"crowdsec-bouncer-traefik-plugin","version":"v1.8.1","last_pull":"2026-11-10T09:00:00Z","revoked":false}]`))
	if err != nil || len(got) != 1 || got[0].Name != "traefik-console-op-1" || got[0].Version != "v1.8.1" {
		t.Fatalf("%v %v", got, err)
	}
	if none, err := parseBouncers([]byte("null")); err != nil || len(none) != 0 {
		t.Fatal("null is an empty list")
	}
}
