package catalog_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-sync/internal/catalog"
)

const sampleDoc = `{
  "openai": {"id": "openai", "name": "OpenAI", "models": {
    "gpt-4": {"id": "gpt-4", "name": "GPT-4", "cost": {"input": 1.5, "output": 2}},
    "gpt-4o": {"id": "gpt-4o", "name": "GPT-4o"}
  }},
  "anthropic": {"id": "anthropic", "name": "Anthropic"}
}`

// TestParseAbsentModelsVsZeroModels covers V1: a provider without a models map
// yields an empty (non-nil) model set, and an explicit empty map behaves the
// same; both differ from a missing provider.
func TestParseAbsentModelsVsZeroModels(t *testing.T) {
	c, err := catalog.Parse([]byte(sampleDoc))
	if err != nil {
		t.Fatal(err)
	}
	if ids := c.ProviderIDs(); len(ids) != 2 || ids[0] != "anthropic" || ids[1] != "openai" {
		t.Fatalf("ProviderIDs = %v", ids)
	}
	if got := c.ModelIDs("anthropic"); len(got) != 0 {
		t.Fatalf("absent models = %v, want empty", got)
	}
	if got := c.ModelIDs("missing"); len(got) != 0 {
		t.Fatalf("missing provider = %v, want empty", got)
	}
	if got := c.ModelIDs("openai"); len(got) != 2 || got[0] != "gpt-4" || got[1] != "gpt-4o" {
		t.Fatalf("openai models = %v", got)
	}

	c2, err := catalog.Parse([]byte(`{"p1":{"name":"P","models":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.ModelIDs("p1"); len(got) != 0 {
		t.Fatalf("explicit empty models = %v", got)
	}
	if _, ok := c2.Providers["p1"]; !ok || c2.Providers["p1"].Models == nil {
		t.Fatalf("models map must be non-nil, got %+v", c2.Providers["p1"])
	}
}

// TestCostStringMissingVsZero covers V1: an absent price is reported as unknown
// or "?", never as a zero price.
func TestCostStringMissingVsZero(t *testing.T) {
	c, err := catalog.Parse([]byte(sampleDoc))
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.CostString(c.Providers["openai"].Models["gpt-4o"].Cost); got != "cost unknown" {
		t.Fatalf("missing cost = %q, want cost unknown", got)
	}
	got := catalog.CostString(c.Providers["openai"].Models["gpt-4"].Cost)
	if !strings.Contains(got, "in 1.5 / out 2") || !strings.Contains(got, "cache read ?") {
		t.Fatalf("partial cost = %q", got)
	}

	z, err := catalog.Parse([]byte(`{"p":{"models":{"m":{"cost":{"input":0,"output":0}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got = catalog.CostString(z.Providers["p"].Models["m"].Cost)
	if !strings.Contains(got, "in 0 / out 0") {
		t.Fatalf("explicit zero cost = %q, want 0 reported", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, body := range []string{
		`not json`,
		`[]`,
		`{}`,
		`{"providers":{}}`,
		`{"p": 3}`,
	} {
		if _, err := catalog.Parse([]byte(body)); err == nil {
			t.Errorf("Parse(%q) = nil error", body)
		}
	}
}

// TestParseAcceptEnvelope covers forward compatibility with {"providers":{...}}.
func TestParseAcceptEnvelope(t *testing.T) {
	c, err := catalog.Parse([]byte(`{"providers":{"p1":{"name":"P","models":{"m":{}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Providers["p1"]; !ok {
		t.Fatalf("providers = %v", c.Providers)
	}
}

func TestLookupIgnoresVariant(t *testing.T) {
	c, err := catalog.Parse([]byte(sampleDoc))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Verifies("openai/gpt-4") || !c.Verifies("openai/gpt-4#high") {
		t.Fatal("expected gpt-4 and its variant to verify")
	}
	if c.Verifies("openai/nope") || c.Verifies("ghost/gpt-4") || c.Verifies("openai") {
		t.Fatal("unexpected verification")
	}
}

func TestFetchOKAndErrors(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleDoc))
	}))
	defer ok.Close()
	c, err := catalog.Fetch(context.Background(), ok.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if c.Verifies("anthropic") || !c.Verifies("openai/gpt-4") {
		t.Fatal("unexpected catalog contents")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := catalog.Fetch(context.Background(), bad.URL); err == nil {
		t.Fatal("expected an error for a non-200 status")
	}

	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not json</html>"))
	}))
	defer malformed.Close()
	if _, err := catalog.Fetch(context.Background(), malformed.URL); err == nil {
		t.Fatal("expected an error for a malformed body")
	}
}

// TestFetchHonorsCanceledContext covers V2: a canceled context aborts the fetch
// instead of hanging for the full timeout.
func TestFetchHonorsCanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleDoc))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := catalog.Fetch(ctx, srv.URL); err == nil {
		t.Fatal("expected a canceled-context error")
	}
}
