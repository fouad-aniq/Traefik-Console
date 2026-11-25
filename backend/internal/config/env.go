package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TraefikACMEDir is where Traefik stores ACME (Let's Encrypt) files; resolvers created
// by the Console point their storage here.
const TraefikACMEDir = "/etc/traefik/acme"

// Load parses and validates environment settings without creating files or keys.
func Load() (*Options, error) {
	port := getString("PORT", "8081")
	if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("invalid PORT")
	}

	bind := getString("BIND_ADDRESS", "127.0.0.1")
	if net.ParseIP(bind) == nil {
		return nil, fmt.Errorf("BIND_ADDRESS must be an IP literal")
	}

	dbPath, err := filepath.Abs(getString("DB_PATH", "./data/console.db"))
	if err != nil {
		return nil, fmt.Errorf("%w: DB_PATH: %v", ErrInvalidConfigPath, err)
	}

	// Folder settings come in pairs: NAME is the path inside the Console container,
	// TRAEFIK_NAME is the same folder as Traefik sees it (used in generated YAML).
	for _, renamed := range [][2]string{{"TRAEFIK_STATIC_PATH", "STATIC_FILE"}, {"TRAEFIK_RUNTIME_DYNAMIC_DIR", "TRAEFIK_DYNAMIC_DIR"}} {
		if strings.TrimSpace(os.Getenv(renamed[0])) != "" {
			return nil, fmt.Errorf("%s was renamed to %s", renamed[0], renamed[1])
		}
	}
	// ACME storage is not configurable: Traefik keeps it in TraefikACMEDir and the
	// Console reads the same folder as "acme" next to its dynamic folder.
	for _, removed := range []string{"ACME_OBSERVATION_DIR", "ACME_DIR", "TRAEFIK_ACME_DIR"} {
		if strings.TrimSpace(os.Getenv(removed)) != "" {
			return nil, fmt.Errorf("%s is no longer used: ACME files live in the acme folder next to the dynamic folder (%s inside Traefik)", removed, TraefikACMEDir)
		}
	}

	staticPath, err := filepath.Abs(getString("STATIC_FILE", "./traefik/traefik.yml"))
	if err != nil {
		return nil, fmt.Errorf("%w: STATIC_FILE: %v", ErrInvalidConfigPath, err)
	}

	dynamicDir, err := filepath.Abs(getString("DYNAMIC_DIR", "./traefik/dynamic"))
	if err != nil {
		return nil, fmt.Errorf("%w: DYNAMIC_DIR: %v", ErrInvalidConfigPath, err)
	}

	if strings.TrimSpace(os.Getenv("TRAEFIK_ACME_PATH")) != "" {
		return nil, fmt.Errorf("TRAEFIK_ACME_PATH is obsolete: ACME files live in the acme folder next to the dynamic folder (%s inside Traefik)", TraefikACMEDir)
	}

	secretsDir, err := filepath.Abs(getString("SECRETS_DIR", "./traefik/secrets"))
	if err != nil {
		return nil, fmt.Errorf("%w: SECRETS_DIR: %v", ErrInvalidConfigPath, err)
	}

	keyFile, err := filepath.Abs(getString("MASTER_KEY_FILE", "./keys/master.key"))
	if err != nil {
		return nil, fmt.Errorf("%w: MASTER_KEY_FILE: %v", ErrInvalidConfigPath, err)
	}

	dockerOptions, err := loadDockerOptions()
	if err != nil {
		return nil, err
	}

	probeTargets := parseProbeTargets(getString("TRAEFIK_PROBE_TARGETS", "websecure=127.0.0.1:443"))

	runtimeSecrets := getString("TRAEFIK_SECRETS_DIR", "/etc/traefik/secrets")
	if !filepath.IsAbs(runtimeSecrets) || runtimeSecrets == "/" {
		return nil, fmt.Errorf("traefik runtime mappings require absolute directory roots")
	}
	// Certificates are listed only when that acme folder exists; otherwise they are reported as not mapped.
	acmeDir := filepath.Join(filepath.Dir(dynamicDir), "acme")
	if st, e := os.Stat(acmeDir); e != nil || !st.IsDir() {
		acmeDir = ""
	}
	timeoutSec := getInt("TRAEFIK_CONVERGENCE_TIMEOUT_SEC", 15)
	if timeoutSec > 60 {
		return nil, fmt.Errorf("convergence timeout exceeds 60 seconds")
	}

	journalLimit := int64(512 * 1024 * 1024)
	if raw := strings.TrimSpace(os.Getenv("MAX_JOURNAL_BYTES")); raw != "" {
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 1024*1024 || n > 1<<40 {
			return nil, fmt.Errorf("MAX_JOURNAL_BYTES must be between 1 MiB and 1 TiB")
		}
		journalLimit = n
	}
	snapshotLimit := int64(1024 * 1024 * 1024)
	if raw := strings.TrimSpace(os.Getenv("MAX_SNAPSHOT_BYTES")); raw != "" {
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 1024*1024 || n > 1<<40 {
			return nil, fmt.Errorf("MAX_SNAPSHOT_BYTES must be between 1 MiB and 1 TiB")
		}
		snapshotLimit = n
	}
	retention := getInt("SNAPSHOT_RETENTION_AUTO", 20)
	if retention < 0 || retention > 1000 {
		return nil, fmt.Errorf("SNAPSHOT_RETENTION_AUTO must be between 0 and 1000")
	}
	historyDays := getInt("OPERATION_HISTORY_DAYS", 90)
	if historyDays < 1 || historyDays > 3650 {
		return nil, fmt.Errorf("OPERATION_HISTORY_DAYS must be between 1 and 3650")
	}
	// Off by default: every operation already rolls back on failure, and the first start
	// records an initial snapshot. Set AUTO_SNAPSHOT_BEFORE_STATIC=true for one per static change.
	autoSnapshot := false
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("AUTO_SNAPSHOT_BEFORE_STATIC"))); raw != "" {
		switch raw {
		case "true", "1", "yes":
			autoSnapshot = true
		case "false", "0", "no":
			autoSnapshot = false
		default:
			return nil, fmt.Errorf("AUTO_SNAPSHOT_BEFORE_STATIC must be true or false")
		}
	}
	for raw := range strings.SplitSeq(getString("CROWDSEC_PROTECTED_CIDRS", "127.0.0.0/8,::1/128"), ",") {
		if _, _, e := net.ParseCIDR(raw); e != nil {
			return nil, fmt.Errorf("invalid CROWDSEC_PROTECTED_CIDRS")
		}
	}
	readTimeout := getInt("CROWDSEC_READ_TIMEOUT", 20)
	readTimeout = min(max(readTimeout, 1), 120)
	alertLimit := getInt("CROWDSEC_ALERT_LIMIT", 500)
	alertLimit = min(max(alertLimit, 1), 1000)
	crowdsecEnvironment := map[string]string{}
	for _, name := range CrowdSecEnvironmentVariables {
		if raw := os.Getenv(name); strings.TrimSpace(raw) != "" {
			crowdsecEnvironment[name] = raw
		}
	}
	authOptions, err := loadAuthOptions(dbPath)
	if err != nil {
		return nil, err
	}
	return &Options{
		Auth:                     authOptions,
		BindAddress:              bind,
		ConsoleHost:              strings.TrimSpace(os.Getenv("CONSOLE_HOST")),
		ConsoleOrigin:            strings.TrimRight(os.Getenv("CONSOLE_ORIGIN"), "/"),
		JournalLimitBytes:        journalLimit,
		SnapshotLimitBytes:       snapshotLimit,
		SnapshotRetentionAuto:    retention,
		OperationHistoryDays:     historyDays,
		AutoSnapshotBeforeStatic: autoSnapshot,
		Port:                     port,
		DBPath:                   dbPath,
		TraefikStaticPath:        staticPath,
		TraefikDynamicDir:        dynamicDir,
		ACMEObservationDir:       acmeDir,
		RuntimeACMEDir:           TraefikACMEDir,

		RuntimeSecretsDir:   filepath.Clean(runtimeSecrets),
		SecretsDir:          secretsDir,
		MasterKeyFile:       keyFile,
		TraefikAPIURL:       strings.TrimRight(getString("TRAEFIK_API_URL", "http://127.0.0.1:8080"), "/"),
		VerifyConvergenceTo: time.Duration(timeoutSec) * time.Second,
		Docker:              dockerOptions,
		ProbeTargets:        probeTargets,
		ProtectedCIDRs:      strings.Split(getString("CROWDSEC_PROTECTED_CIDRS", "127.0.0.0/8,::1/128"), ","),
		CrowdSec: CrowdSecOptions{
			LAPIURL:           strings.TrimRight(strings.TrimSpace(os.Getenv("CROWDSEC_LAPI_URL")), "/"),
			APIKey:            strings.TrimSpace(os.Getenv("CROWDSEC_API_KEY")),
			MachineID:         strings.TrimSpace(os.Getenv("CROWDSEC_MACHINE_ID")),
			MachinePassword:   os.Getenv("CROWDSEC_MACHINE_PASSWORD"),
			CAFile:            strings.TrimSpace(os.Getenv("CROWDSEC_CA_CERT")),
			ClientCertFile:    strings.TrimSpace(os.Getenv("CROWDSEC_CLIENT_CERT")),
			ClientKeyFile:     strings.TrimSpace(os.Getenv("CROWDSEC_CLIENT_KEY")),
			BouncerCertFile:   strings.TrimSpace(os.Getenv("CROWDSEC_BOUNCER_CERT")),
			BouncerKeyFile:    strings.TrimSpace(os.Getenv("CROWDSEC_BOUNCER_KEY")),
			MachineCertFile:   strings.TrimSpace(os.Getenv("CROWDSEC_MACHINE_CERT")),
			MachineKeyFile:    strings.TrimSpace(os.Getenv("CROWDSEC_MACHINE_KEY")),
			AppSecURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("CROWDSEC_APPSEC_URL")), "/"),
			MetricsURL:        strings.TrimSpace(os.Getenv("CROWDSEC_METRICS_URL")),
			MetricsToken:      os.Getenv("CROWDSEC_METRICS_TOKEN"),
			AccessLogSource:   strings.TrimSpace(os.Getenv("CROWDSEC_ACCESS_LOG_SOURCE")),
			ReadTimeout:       time.Duration(readTimeout) * time.Second,
			AlertLimit:        alertLimit,
			ReadTimeoutSet:    strings.TrimSpace(os.Getenv("CROWDSEC_READ_TIMEOUT")) != "",
			AlertLimitSet:     strings.TrimSpace(os.Getenv("CROWDSEC_ALERT_LIMIT")) != "",
			ProtectedCIDRsSet: strings.TrimSpace(os.Getenv("CROWDSEC_PROTECTED_CIDRS")) != "",
			Environment:       crowdsecEnvironment,
		},
	}, nil
}

func getString(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func getInt(key string, fallback int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(val)
	if err != nil || parsed <= 0 {
		return fallback
	}

	return parsed
}

func parseProbeTargets(raw string) map[string]string {
	out := make(map[string]string)
	for pair := range strings.SplitSeq(raw, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(pair), "="); ok {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k != "" && v != "" {
				out[k] = v
			}
		}
	}
	return out
}
