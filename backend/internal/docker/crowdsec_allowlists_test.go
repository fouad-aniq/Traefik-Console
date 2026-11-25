package docker

import "testing"

func TestAllowlistParsingAndValidation(t *testing.T) {
	list, err := parseAllowlist([]byte(`{"name":"traefik-console-safe","description":"Trusted lab","items":[{"value":"192.0.2.0/24","description":"test network"}],"console_managed":false}`))
	if err != nil || list.Name != "traefik-console-safe" || len(list.Items) != 1 || list.ConsoleManaged {
		t.Fatalf("unexpected allowlist: %+v %v", list, err)
	}
	for _, raw := range []string{`{"name":"bad name","items":[]}`, `{"name":"safe","items":[{"value":"not-an-ip"}]}`, `{"name":"safe","items":[{"value":"192.0.2.1","description":"bad\nvalue"}]}`} {
		if _, err := parseAllowlist([]byte(raw)); err == nil {
			t.Fatalf("unsafe allowlist accepted: %s", raw)
		}
	}
	for _, invalid := range []string{"", "-exec", "name with space", "name;rm"} {
		if validateAllowlistName(invalid) == nil {
			t.Fatalf("unsafe name accepted: %q", invalid)
		}
	}
	for _, invalid := range []string{"", "192.0.2.1/99", "example.invalid", "192.0.2.1;--all"} {
		if validateAllowlistValue(invalid) == nil {
			t.Fatalf("unsafe value accepted: %q", invalid)
		}
	}
}

func TestMachineAndDecisionIdentitiesAreFixed(t *testing.T) {
	if got := machineName(42); got != "traefik-console-machine-42" {
		t.Fatalf("unexpected machine name: %s", got)
	}
	if decisionFlag("192.0.2.1") != "--ip" || decisionFlag("192.0.2.0/24") != "--range" {
		t.Fatal("decision address kind was not preserved")
	}
}
