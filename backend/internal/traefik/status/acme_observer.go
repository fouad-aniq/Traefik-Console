package status

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/traefik/validation"
)

type ACMEObserver struct{ directory, runtimeRoot string }
type ACMEStorageObservation struct {
	Resolver       string `json:"resolver"`
	State          string `json:"state"`
	InvalidEntries int    `json:"invalid_entries"`
	// InUse is filled by the API: a router or domain requests certificates from this resolver.
	// It is true when that cannot be determined, so no storage warning is hidden.
	InUse bool `json:"in_use"`
}
type ACMEInventory struct {
	Certificates []CertificateInfo        `json:"certificates"`
	Observations []ACMEStorageObservation `json:"observations"`
	Partial      bool                     `json:"partial"`
}

func NewACMEObserver(directory, runtimeRoot string) *ACMEObserver {
	return &ACMEObserver{directory: directory, runtimeRoot: filepath.Clean(runtimeRoot)}
}
func (o *ACMEObserver) Observe(static []byte) ACMEInventory {
	out := ACMEInventory{Certificates: []CertificateInfo{}, Observations: []ACMEStorageObservation{}}
	doc, _, e := yaml.ParseBoundedYAML(bytes.NewReader(static), yaml.DefaultParserOptions())
	if e != nil {
		out.Partial = true
		return out
	}
	resolvers := yaml.GetMappingValue(yaml.GetRootMapping(doc), "certificatesResolvers")
	if resolvers == nil {
		return out
	}
	type parsed struct {
		blocks map[string]acmeResolverBlock
		state  string
	}
	cache := map[string]parsed{}
	now := time.Now().UTC()
	for i := 0; i+1 < len(resolvers.Content); i += 2 {
		name := resolvers.Content[i].Value
		acme := yaml.GetMappingValue(resolvers.Content[i+1], "acme")
		if acme == nil {
			continue
		}
		obs := ACMEStorageObservation{Resolver: name}
		storage := yaml.GetMappingValue(acme, "storage")
		if storage == nil || storage.Value == "" {
			obs.State = "not_configured"
		} else if o == nil || o.directory == "" {
			obs.State = "not_mapped"
		} else {
			path := filepath.Clean(storage.Value)
			rel, e := filepath.Rel(o.runtimeRoot, path)
			if e != nil || !filepath.IsAbs(path) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Base(rel) != rel {
				obs.State = "not_mapped"
			} else {
				p, ok := cache[rel]
				if !ok {
					raw, state := o.readStable(rel)
					p = parsed{state: state}
					if state == "observed" {
						if json.Unmarshal(raw, &p.blocks) != nil || p.blocks == nil {
							p.state = "invalid"
						}
					}
					cache[rel] = p
				}
				obs.State = p.state
				if p.state == "observed" {
					block, exists := p.blocks[name]
					if !exists {
						obs.State = "uninitialized"
					} else if len(block.Certificates) == 0 {
						obs.State = "empty"
					} else {
						for _, entry := range block.Certificates {
							cert, e := parseBase64Cert(name, entry.Domain.Main, entry.Domain.SANs, entry.Certificate, now)
							if e != nil {
								obs.InvalidEntries++
								continue
							}
							out.Certificates = append(out.Certificates, cert)
						}
						if obs.InvalidEntries > 0 {
							obs.State = "incomplete"
						}
					}
				}
			}
		}
		if obs.State != "observed" && obs.State != "empty" {
			out.Partial = true
		}
		out.Observations = append(out.Observations, obs)
	}
	sort.Slice(out.Observations, func(i, j int) bool { return out.Observations[i].Resolver < out.Observations[j].Resolver })
	sort.Slice(out.Certificates, func(i, j int) bool { return out.Certificates[i].ExpiresAt.Before(out.Certificates[j].ExpiresAt) })
	return out
}
func (o *ACMEObserver) readStable(name string) ([]byte, string) {
	root, e := os.OpenRoot(o.directory)
	if e != nil {
		return nil, "unreadable"
	}
	defer root.Close()
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(e, os.ErrNotExist) {
		return nil, "missing"
	}
	if e != nil {
		return nil, "unreadable"
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil || !before.Mode().IsRegular() || before.Size() > maxAcmeFileSize {
		return nil, "invalid"
	}
	raw, e := io.ReadAll(io.LimitReader(f, maxAcmeFileSize+1))
	if e != nil {
		return nil, "unreadable"
	}
	after, e := f.Stat()
	if e != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, "changing"
	}
	if len(raw) > maxAcmeFileSize {
		return nil, "invalid"
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, "uninitialized"
	}
	return raw, "observed"
}
func (i ACMEInventory) Find(resolver string, hosts []string) *CertificateInfo {
	for _, c := range i.Certificates {
		if c.ResolverName != resolver {
			continue
		}
		all := true
		for _, h := range hosts {
			if !validation.NamesCover(append([]string{c.MainDomain}, c.SANs...), h) {
				all = false
				break
			}
		}
		if all {
			return &c
		}
	}
	return nil
}
