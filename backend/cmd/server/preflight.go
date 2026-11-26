package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/traefik/status"
)

func runPreflight() error {
	opts, runtime, err := loadOptions()
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := checkLayout(opts); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observer := status.NewObservationClient(opts.TraefikAPIURL)
	defer observer.Close()
	if _, err := observer.FetchRawData(ctx); err != nil {
		return errors.New("cannot read the registered private Traefik API /api/rawdata")
	}
	cliOut.Infof("Preflight passed: file provider, registered directories, state and private Traefik API")
	return nil
}

// Only stat, bounded reads and access checks are permitted before attach.
func checkLayout(opts *config.Options) error {
	staticDir := filepath.Dir(opts.TraefikStaticPath)
	if under(opts.TraefikDynamicDir, staticDir) || under(opts.TraefikDynamicDir, opts.SecretsDir) || under(opts.TraefikDynamicDir, opts.MasterKeyFile) || under(opts.TraefikDynamicDir, opts.DBPath) || under(opts.TraefikDynamicDir, opts.Auth.DBPath) {
		return errors.New("keep static configuration, secrets and private state outside the watched directory")
	}
	if opts.TraefikStaticPath == filepath.Join(staticDir, files.ConsoleDomainsFilename) {
		return errors.New("static filename collides with Console domain metadata")
	}
	// Console creates a missing watched folder or secrets folder at start (when its parent exists).
	willCreate := func(dir string) bool {
		return (dir == opts.TraefikDynamicDir || dir == opts.SecretsDir) && config.Creatable(dir)
	}
	for _, dir := range []string{staticDir, opts.TraefikDynamicDir, opts.SecretsDir, filepath.Dir(opts.DBPath), filepath.Dir(opts.Auth.DBPath), filepath.Dir(opts.MasterKeyFile)} {
		if willCreate(dir) {
			cliOut.Infof("%s does not exist yet; Console creates it when it starts", dir)
			continue
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || resolved != dir {
			return fmt.Errorf("directory missing or symlinked: %s", dir)
		}
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() || syscall.Access(dir, 7) != nil {
			return fmt.Errorf("directory must be readable, searchable and writable by the Console UID: %s", dir)
		}
	}
	for _, dir := range []string{opts.TraefikDynamicDir, opts.SecretsDir, filepath.Dir(opts.MasterKeyFile)} {
		if willCreate(dir) {
			continue
		}
		parent := filepath.Dir(dir)
		if !sameMount(dir, parent) || syscall.Access(parent, 3) != nil {
			return fmt.Errorf("mount the writable common parent for atomic staging: %s", dir)
		}
	}
	info, err := os.Lstat(opts.TraefikStaticPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("registered static YAML must be an existing regular file")
	}
	if !sameMount(opts.TraefikStaticPath, staticDir) {
		return errors.New("mount the static YAML's parent directory for atomic replacement")
	}
	raw, err := filesystem.ReadBounded(opts.TraefikStaticPath, yaml.DefaultMaxYAMLSize)
	if err != nil {
		return errors.New("cannot read registered static YAML")
	}
	doc, _, err := yaml.ParseBoundedYAML(bytes.NewReader(raw), yaml.DefaultParserOptions())
	if err != nil {
		return errors.New("invalid static YAML")
	}
	var cfg struct {
		Providers struct {
			File struct {
				Directory, Filename string
				Watch               *bool
			}
		}
	}
	if err := doc.Decode(&cfg); err != nil {
		return errors.New("invalid file provider configuration")
	}
	expected := os.Getenv("TRAEFIK_DYNAMIC_DIR")
	if expected == "" {
		expected = "/etc/traefik/dynamic"
	}
	f := cfg.Providers.File
	if f.Directory != expected || f.Filename != "" || (f.Watch != nil && !*f.Watch) {
		return errors.New("Traefik must watch the registered file-provider directory")
	}
	_, err = os.Lstat(opts.DBPath)
	fresh := os.IsNotExist(err)
	if err != nil && !fresh {
		return err
	}
	for _, p := range []string{opts.DBPath, opts.Auth.DBPath, opts.MasterKeyFile, filepath.Join(staticDir, files.ConsoleDomainsFilename), filepath.Join(opts.TraefikDynamicDir, files.ManagedDynamicFilename)} {
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			if !fresh && (p == opts.MasterKeyFile || p == opts.Auth.DBPath) {
				return errors.New("existing state requires its original master key and authentication database")
			}
			continue
		}
		if err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("unsafe registered file: %s", p)
		}
		if fresh {
			return fmt.Errorf("existing reserved file without matching state; restore or review before attaching: %s", p)
		}
		if (p == opts.DBPath || p == opts.Auth.DBPath || p == opts.MasterKeyFile) && st.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("state file must be private: %s", p)
		}
	}
	return nil
}

func under(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// Linux mount IDs distinguish separate bind mounts even on the same device.
// Directories on different mounts cannot exchange files by atomic rename.
func sameMount(a, b string) bool {
	left, err := mountID(a)
	if err != nil {
		return false
	}
	right, err := mountID(b)
	return err == nil && left == right
}

func mountID(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := os.Open(fmt.Sprintf("/proc/self/fdinfo/%d", f.Fd()))
	if err != nil {
		return "", err
	}
	defer info.Close()
	scan := bufio.NewScanner(info)
	for scan.Scan() {
		if id, ok := strings.CutPrefix(scan.Text(), "mnt_id:"); ok {
			return strings.TrimSpace(id), nil
		}
	}
	return "", errors.New("mount ID unavailable")
}
