// Package catalog fetches and parses the public models.dev catalog. It reads
// only the public JSON document over HTTP with a bounded timeout and response
// size, sends no credentials and persists nothing.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DefaultURL is the public catalog endpoint.
const DefaultURL = "https://models.dev/api.json"

// DefaultTimeout bounds a single catalog fetch.
const DefaultTimeout = 15 * time.Second

// maxBody bounds the response body so a hostile or broken endpoint cannot
// exhaust memory.
const maxBody = 64 << 20

// Cost is the price in USD per million tokens. A nil field means the catalog
// did not report that dimension, which is distinct from a reported zero.
type Cost struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
}

// Model is one catalog model.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Cost Cost   `json:"cost"`
}

// Provider is one catalog provider.
type Provider struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Models map[string]Model `json:"models"`
}

// Catalog is the parsed provider map.
type Catalog struct {
	Providers map[string]Provider `json:"-"`
}

type providerJSON struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Models map[string]Model `json:"models"`
}

// Fetch downloads and parses the catalog from url.
func Fetch(ctx context.Context, url string) (*Catalog, error) {
	if url == "" {
		url = DefaultURL
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching model catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching model catalog: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("reading model catalog: %w", err)
	}
	return Parse(body)
}

// Parse decodes the catalog document. The live document is an object keyed by
// provider id; for forward compatibility a single {"providers": {...}} envelope
// is also accepted.
func Parse(body []byte) (*Catalog, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("model catalog is not valid JSON: %w", err)
	}
	providers := root
	if len(root) == 1 {
		if raw, ok := root["providers"]; ok {
			var env map[string]json.RawMessage
			if err := json.Unmarshal(raw, &env); err == nil {
				providers = env
			}
		}
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("model catalog has no providers")
	}
	out := &Catalog{Providers: map[string]Provider{}}
	for id, raw := range providers {
		var p providerJSON
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("provider %q: %w", id, err)
		}
		if p.ID == "" {
			p.ID = id
		}
		if p.Models == nil {
			p.Models = map[string]Model{}
		}
		out.Providers[id] = Provider{ID: p.ID, Name: p.Name, Models: p.Models}
	}
	return out, nil
}

// ProviderIDs returns the provider ids in sorted order.
func (c *Catalog) ProviderIDs() []string {
	ids := make([]string, 0, len(c.Providers))
	for id := range c.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ModelIDs returns the model ids of a provider in sorted order.
func (c *Catalog) ModelIDs(provider string) []string {
	p, ok := c.Providers[provider]
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(p.Models))
	for id := range p.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Lookup resolves provider/model (the variant is ignored) and reports the
// provider and model when both are present in the catalog.
func (c *Catalog) Lookup(ref string) (prov Provider, model Model, ok bool) {
	i := strings.IndexByte(ref, '/')
	if i <= 0 {
		return Provider{}, Model{}, false
	}
	provID := ref[:i]
	modelID := ref[i+1:]
	if j := strings.IndexByte(modelID, '#'); j >= 0 {
		modelID = modelID[:j]
	}
	p, ok := c.Providers[provID]
	if !ok {
		return Provider{}, Model{}, false
	}
	m, ok := p.Models[modelID]
	if !ok {
		return Provider{}, Model{}, false
	}
	return p, m, true
}

// Verifies reports whether ref resolves to a catalog entry.
func (c *Catalog) Verifies(ref string) bool {
	_, _, ok := c.Lookup(ref)
	return ok
}

// CostString renders a catalog price informatively. It never presents an
// absent price as zero.
func CostString(c Cost) string {
	f := func(v *float64) string {
		if v == nil {
			return "?"
		}
		return trimFloat(*v)
	}
	if c.Input == nil && c.Output == nil && c.CacheRead == nil && c.CacheWrite == nil {
		return "cost unknown"
	}
	s := fmt.Sprintf("in %s / out %s USD per Mtok (cache read %s / write %s)", f(c.Input), f(c.Output), f(c.CacheRead), f(c.CacheWrite))
	return s
}

func trimFloat(v float64) string {
	s := fmt.Sprintf("%.6f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}
