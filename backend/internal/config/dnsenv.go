package config

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DNSProvidersEnvFile is Traefik's DNS provider env file, kept by the Console next to Traefik's
// dynamic folder. Traefik reads DNS provider credentials only from its environment; the start
// command in the shipped Compose files exports this file's NAME_FILE lines on every start, so the
// Console adds the variables a resolver needs here and the restart it already does loads them.
const DNSProvidersEnvFile = "dns-providers.env"

const maxDNSEnvBytes = 64 * 1024

var dnsEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*_FILE$`)

const dnsEnvHeader = `# Managed by Traefik Console: DNS provider credentials for certificate resolvers.
# Traefik exports these lines when it starts; each points to a file in the secrets folder.
# Console adds lines when a resolver needs them. No need to edit this file.
`

// DNSEnvFile is the env file as the Console sees it (Path) and the secrets folder as Traefik sees
// it (SecretsRoot); only NAME_FILE lines pointing inside SecretsRoot are read or written.
type DNSEnvFile struct {
	Path        string
	SecretsRoot string
}

func (f *DNSEnvFile) valid(key, value string) bool {
	return dnsEnvName.MatchString(key) && strings.HasPrefix(value, f.SecretsRoot+"/") &&
		!strings.Contains(value, "..") && !strings.ContainsAny(value, "\r\n\"'`$\\ \t")
}

func (f *DNSEnvFile) read() ([]byte, error) {
	st, err := os.Stat(f.Path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxDNSEnvBytes {
		return nil, fmt.Errorf("%s is not a regular file under %d bytes", f.Path, maxDNSEnvBytes)
	}
	return os.ReadFile(f.Path)
}

func parseDNSEnvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	key, value, ok := strings.Cut(line, "=")
	return strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`), ok
}

// Load returns the valid NAME_FILE lines; a missing or unreadable file yields nil.
func (f *DNSEnvFile) Load() map[string]string {
	if f == nil {
		return nil
	}
	raw, err := f.read()
	if err != nil {
		return nil
	}
	out := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		if key, value, ok := parseDNSEnvLine(scanner.Text()); ok && f.valid(key, value) {
			out[key] = value
		}
	}
	return out
}

// Ensure makes the file declare every variable in want (NAME_FILE -> path), creating the file when
// missing, replacing a line whose path differs and keeping every other line. It reports whether the
// file changed. The write is atomic (temporary file, then rename).
func (f *DNSEnvFile) Ensure(want map[string]string) (bool, error) {
	if f == nil || len(want) == 0 {
		return false, nil
	}
	for key, value := range want {
		if !f.valid(key, value) {
			return false, fmt.Errorf("refusing DNS env line %q", key)
		}
	}
	raw, err := f.read()
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	var lines []string
	if len(raw) == 0 {
		lines = strings.Split(strings.TrimSuffix(dnsEnvHeader, "\n"), "\n")
	} else {
		lines = strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
	done := map[string]bool{}
	changed := false
	for i, line := range lines {
		key, value, ok := parseDNSEnvLine(line)
		target, wanted := want[key]
		if !ok || !wanted || done[key] {
			continue
		}
		done[key] = true
		if value != target {
			lines[i], changed = key+"="+target, true
		}
	}
	var missing []string
	for key := range want {
		if !done[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		lines, changed = append(lines, key+"="+want[key]), true
	}
	if !changed && len(raw) > 0 {
		return false, nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".dns-providers-*.env")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.WriteString(strings.Join(lines, "\n") + "\n"); err == nil {
		err = tmp.Chmod(0o644)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), f.Path)
}
