package config

import (
	"bufio"
	"bytes"
	"os"
	"strings"
)

// DNSProvidersEnvFile is the env file shipped next to Traefik's dynamic folder. Traefik loads it
// (Compose env_file) so every DNS provider credential is declared as NAME_FILE pointing into the
// secrets folder; the Console only writes the secret files those variables name.
const DNSProvidersEnvFile = "dns-providers.env"

const maxPresetEnvBytes = 64 * 1024

// loadPresetSecretEnv reads NAME_FILE=path lines whose path lies inside secretsRoot. Anything else
// (comments, other variables, paths elsewhere) is ignored. A missing or oversized file yields nil.
func loadPresetSecretEnv(path, secretsRoot string) map[string]string {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxPresetEnvBytes {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`)
		if !ok || !strings.HasSuffix(key, "_FILE") || !strings.HasPrefix(value, secretsRoot+"/") || strings.Contains(value, "..") {
			continue
		}
		out[key] = value
	}
	return out
}
