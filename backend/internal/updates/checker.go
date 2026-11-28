// Package updates tells the Console whether a newer release is published on GitHub.
package updates

import (
	"context"
	"encoding/json"
	"github.com/traefik-console/internal/project"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Release is a newer published version and its release notes page.
type Release struct {
	Version string
	URL     string
}

// Checker reads GitHub's release list on demand and caches the answer for MaxAge. Callers never
// wait for the network: Latest answers from the cache and starts a refresh in the background when
// the cache is stale, so a page that polls every few seconds sees a new release within MaxAge.
// Requests carry the previous ETag; GitHub answers "not modified" when nothing changed.
type Checker struct {
	current semver
	// Pre-release builds also look at pre-releases; a stable build only at stable releases.
	includePre bool

	URL    string
	Client *http.Client
	MaxAge time.Duration
	now    func() time.Time

	mu       sync.Mutex
	checked  time.Time
	running  bool
	etag     string
	newer    *Release
	disabled bool
}

// New returns a checker for the running version, or nil when updates cannot be compared (a local
// "dev" build) or the check is turned off.
func New(currentVersion string, enabled bool) *Checker {
	current, ok := parseSemver(currentVersion)
	if !enabled || !ok {
		return nil
	}
	return &Checker{
		current:    current,
		includePre: current.prerelease(),
		URL:        project.ReleasesAPIURL(),
		Client:     &http.Client{Timeout: 15 * time.Second},
		MaxAge:     project.UpdateCheckInterval,
		now:        time.Now,
	}
}

// Latest returns the newest release above the running version, if one is known, and refreshes the
// cache in the background when it is older than MaxAge. It never blocks on the network.
func (c *Checker) Latest() *Release {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running && c.now().Sub(c.checked) >= c.MaxAge {
		c.running = true
		go c.refresh()
	}
	if c.newer == nil {
		return nil
	}
	r := *c.newer
	return &r
}

// Refresh checks GitHub now and waits for the answer (used at startup and by tests).
func (c *Checker) Refresh(ctx context.Context) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()
	c.refreshWith(ctx)
}

func (c *Checker) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c.refreshWith(ctx)
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func (c *Checker) refreshWith(ctx context.Context) {
	c.mu.Lock()
	etag := c.etag
	c.mu.Unlock()

	newer, newEtag, changed := c.fetch(ctx, etag)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	c.checked = c.now()
	if changed {
		c.newer, c.etag = newer, newEtag
	}
}

// fetch returns the best newer release; changed is false when the cached answer still holds
// (not modified, or the request failed: a network error keeps the last known answer).
func (c *Checker) fetch(ctx context.Context, etag string) (*Release, string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, "", false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", project.UserAgent())
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", false
	}
	var releases []githubRelease
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&releases) != nil {
		return nil, "", false
	}
	var best *Release
	var bestVersion semver
	for _, r := range releases {
		v, ok := parseSemver(r.TagName)
		if !ok || r.TagName != strings.TrimSpace(r.TagName) || r.Draft || (!c.includePre && (r.Prerelease || v.prerelease())) || v.compare(c.current) <= 0 {
			continue
		}
		if best == nil || v.compare(bestVersion) > 0 {
			// The link is built from the validated tag, never taken from the response.
			best, bestVersion = &Release{Version: trimV(r.TagName), URL: project.ReleasePageURL(r.TagName)}, v
		}
	}
	return best, resp.Header.Get("ETag"), true
}

func trimV(tag string) string {
	if len(tag) > 0 && tag[0] == 'v' {
		return tag[1:]
	}
	return tag
}
