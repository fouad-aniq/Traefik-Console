package engine

import "testing"

// Taking over an entrypoint as it is renders the same data in another format: no rewrite and
// no restart. A real change still restarts Traefik.
func TestSameYAMLIgnoresFormattingButNotData(t *testing.T) {
	file := []byte("# my comments\nentryPoints:\n  web-secure:\n    address: \":443\"   # TLS\n    http:\n      tls:\n        options: tls-opts@file\n  web:\n    address: \":80\"\n")
	rendered := []byte("entryPoints:\n  web:\n    address: :80\n  web-secure:\n    address: :443\n    http:\n      tls:\n        options: tls-opts@file\n")
	if !sameYAML(file, rendered) {
		t.Fatal("same data in another format must count as unchanged")
	}
	if sameYAML(file, []byte("entryPoints:\n  web:\n    address: :81\n")) || sameYAML([]byte("a: ["), file) {
		t.Fatal("different data or invalid YAML must count as changed")
	}
}
