package sharedsecrets

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleRegistry = `version: 1
entries:
  ANTHROPIC_API_TOKEN: {kind: api_token, aliases: [ANTHROPIC_API_KEY], exports: [ANTHROPIC_API_KEY], used_by: [scopeweaver]}
  OPENAI_API_TOKEN:    {kind: api_token, aliases: [OPENAI_API_KEY], used_by: [scopeweaver]}
  DISCORD_WEBHOOK_ARTEX: {kind: webhook, used_by: [artex]}
`

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestStoreGetCanonicalAndAlias(t *testing.T) {
	dir := t.TempDir()
	reg := writeFile(t, dir, "registry.yaml", sampleRegistry)
	// Canonical name present; a legacy alias also present for a second vendor.
	sec := writeFile(t, dir, "secrets.yaml", `version: 1
ANTHROPIC_API_TOKEN: "anth-canonical"
OPENAI_API_KEY: "oai-legacy-alias"
DISCORD_WEBHOOK_ARTEX: "https://discord.example/webhook/artex"
`)
	s := LoadFrom(sec, reg)

	if got := s.Get("ANTHROPIC_API_TOKEN"); got != "anth-canonical" {
		t.Fatalf("canonical: got %q", got)
	}
	// Value lives only under the legacy alias; the registry must bridge it.
	if got := s.Get("OPENAI_API_TOKEN"); got != "oai-legacy-alias" {
		t.Fatalf("alias fallback: got %q", got)
	}
	if got := s.Get("DISCORD_WEBHOOK_ARTEX"); got != "https://discord.example/webhook/artex" {
		t.Fatalf("webhook: got %q", got)
	}
	if got := s.Get("NOT_PRESENT"); got != "" {
		t.Fatalf("missing name should be empty, got %q", got)
	}
}

func TestMissingFilesDegradeGracefully(t *testing.T) {
	s := LoadFrom("/nonexistent/secrets.yaml", "/nonexistent/registry.yaml")
	if got := s.Get("ANTHROPIC_API_TOKEN"); got != "" {
		t.Fatalf("missing files should yield empty, got %q", got)
	}
	var nilStore *Store
	if got := nilStore.Get("ANTHROPIC_API_TOKEN"); got != "" {
		t.Fatalf("nil store should yield empty, got %q", got)
	}
}

func TestEmptyValueFallsThroughToAlias(t *testing.T) {
	dir := t.TempDir()
	reg := writeFile(t, dir, "registry.yaml", sampleRegistry)
	// Canonical name is present but empty; the alias carries the real value.
	sec := writeFile(t, dir, "secrets.yaml", `version: 1
OPENAI_API_TOKEN: ""
OPENAI_API_KEY: "from-alias"
`)
	s := LoadFrom(sec, reg)
	if got := s.Get("OPENAI_API_TOKEN"); got != "from-alias" {
		t.Fatalf("empty canonical should fall through to alias, got %q", got)
	}
}

func TestMalformedRegistryStillRecoversAliases(t *testing.T) {
	dir := t.TempDir()
	// The second line is invalid YAML (no space after the colon, as seen in the
	// shared registry): strict parsing of the whole file fails, so the line scan
	// must still recover the OPENAI_API_TOKEN aliases from the good line.
	reg := writeFile(t, dir, "registry.yaml", `version: 1
entries:
  SOME_DIR:{kind: dir, used_by: [mcp-recon]}
  OPENAI_API_TOKEN: {kind: api_token, aliases: [OPENAI_API_KEY], used_by: [scopeweaver]}
`)
	sec := writeFile(t, dir, "secrets.yaml", `version: 1
OPENAI_API_KEY: "from-legacy-alias"
`)
	s := LoadFrom(sec, reg)
	if got := s.Get("OPENAI_API_TOKEN"); got != "from-legacy-alias" {
		t.Fatalf("alias recovery after malformed registry: got %q", got)
	}
}

func TestValuePrefersEnvOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, registryBasename, sampleRegistry)
	writeFile(t, dir, secretsBasename, `version: 1
ANTHROPIC_API_TOKEN: "from-file"
`)
	t.Setenv(EnvDir, dir)
	// Clear any caller-set location overrides that could shadow EnvDir.
	t.Setenv(EnvFile, "")
	t.Setenv(EnvRegistry, "")

	// No env override: shared file wins.
	t.Setenv("ANTHROPIC_API_KEY", "")
	if got := Value("ANTHROPIC_API_KEY", "ANTHROPIC_API_TOKEN"); got != "from-file" {
		t.Fatalf("file fallback: got %q", got)
	}
	// Explicit env override wins over the file.
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	if got := Value("ANTHROPIC_API_KEY", "ANTHROPIC_API_TOKEN"); got != "from-env" {
		t.Fatalf("env override: got %q", got)
	}
}

func TestDefaultStoreReloadsWhenPathChanges(t *testing.T) {
	dir1 := t.TempDir()
	writeFile(t, dir1, secretsBasename, "version: 1\nSHODAN_API_TOKEN: \"one\"\n")
	t.Setenv(EnvDir, dir1)
	t.Setenv(EnvFile, "")
	t.Setenv(EnvRegistry, "")
	if got := Get("SHODAN_API_TOKEN"); got != "one" {
		t.Fatalf("dir1: got %q", got)
	}
	dir2 := t.TempDir()
	writeFile(t, dir2, secretsBasename, "version: 1\nSHODAN_API_TOKEN: \"two\"\n")
	t.Setenv(EnvDir, dir2)
	if got := Get("SHODAN_API_TOKEN"); got != "two" {
		t.Fatalf("dir2 (reload): got %q", got)
	}
}

func TestExpandString(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, registryBasename, sampleRegistry)
	writeFile(t, dir, secretsBasename, `version: 1
DISCORD_WEBHOOK_ARTEX: "https://discord.example/webhook/artex"
`)
	t.Setenv(EnvDir, dir)
	t.Setenv(EnvFile, "")
	t.Setenv(EnvRegistry, "")

	if got := ExpandString("${DISCORD_WEBHOOK_ARTEX}"); got != "https://discord.example/webhook/artex" {
		t.Fatalf("expand ref: got %q", got)
	}
	// A bare non-reference string is untouched.
	if got := ExpandString("https://explicit.example/hook"); got != "https://explicit.example/hook" {
		t.Fatalf("non-ref unchanged: got %q", got)
	}
	// An unresolved reference stays visible rather than becoming empty.
	if got := ExpandString("${DISCORD_WEBHOOK_MISSING}"); got != "${DISCORD_WEBHOOK_MISSING}" {
		t.Fatalf("unresolved ref should stay visible, got %q", got)
	}
}

func TestExpandRefsMap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, registryBasename, sampleRegistry)
	writeFile(t, dir, secretsBasename, `version: 1
DISCORD_WEBHOOK_ARTEX: "https://discord.example/webhook/artex"
`)
	t.Setenv(EnvDir, dir)
	t.Setenv(EnvFile, "")
	t.Setenv(EnvRegistry, "")

	cfg := map[string]any{
		"url":    "${DISCORD_WEBHOOK_ARTEX}",
		"method": "POST",
		"count":  float64(3),
	}
	ExpandRefs(cfg)
	if cfg["url"] != "https://discord.example/webhook/artex" {
		t.Fatalf("url not expanded: %v", cfg["url"])
	}
	if cfg["method"] != "POST" {
		t.Fatalf("method changed unexpectedly: %v", cfg["method"])
	}
	if cfg["count"] != float64(3) {
		t.Fatalf("non-string changed unexpectedly: %v", cfg["count"])
	}
}
