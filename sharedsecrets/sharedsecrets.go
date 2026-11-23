// Package sharedsecrets reads API tokens, tool paths and webhook URLs from the
// shared bug bounty secrets file (~/.config/bugbounty/secrets.yaml), resolving
// each canonical name against the aliases listed in the name registry
// (~/.config/bugbounty/secrets-registry.yaml). Both files live OUTSIDE the
// ~/bugbounty tree, are flat/registered per programs/_ops/SECRETS-STANDARD.md,
// and are shared by ai-recon, MonRust, mcp-recon and ScopeWeaver.
//
// Everything degrades gracefully: a missing file, an unreadable file or a name
// with no value yields an empty string, never a panic and never an error, so a
// feature that needs a secret simply stays off. The explicit environment
// variable a caller already read stays the override; this package is the
// fallback below it.
package sharedsecrets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Environment overrides for the file locations. They let a container mount the
// shared files anywhere (or a test point at a fixture) without code changes.
const (
	// EnvDir overrides the directory that holds both files.
	EnvDir = "BUGBOUNTY_SECRETS_DIR"
	// EnvFile overrides the full path of the secrets file.
	EnvFile = "BUGBOUNTY_SECRETS_FILE"
	// EnvRegistry overrides the full path of the registry file.
	EnvRegistry = "BUGBOUNTY_SECRETS_REGISTRY"
)

const (
	secretsBasename  = "secrets.yaml"
	registryBasename = "secrets-registry.yaml"
)

// Store is an immutable snapshot of the shared files: the name/value pairs from
// the secrets file and the canonical-to-aliases map from the registry.
type Store struct {
	// values maps every name present in the secrets file (canonical or legacy
	// alias) to its value.
	values map[string]string
	// aliases maps a canonical name to the legacy names the registry lists for
	// it, so a value still stored under an old name is found during migration.
	aliases map[string][]string
}

// Get resolves a canonical name to its value. It first tries the canonical name
// in the secrets file, then each registered alias, and returns "" when none
// carries a value. A nil Store resolves to "".
func (s *Store) Get(name string) string {
	if s == nil {
		return ""
	}
	if v := strings.TrimSpace(s.values[name]); v != "" {
		return v
	}
	for _, a := range s.aliases[name] {
		if v := strings.TrimSpace(s.values[a]); v != "" {
			return v
		}
	}
	return ""
}

// LoadFrom parses the two files at the given paths. Any read or parse failure
// is swallowed into an empty (but non-nil) part, so the returned Store is always
// usable. A missing registry just means no alias fallback.
func LoadFrom(secretsPath, registryPath string) *Store {
	return &Store{
		values:  parseSecrets(secretsPath),
		aliases: parseRegistryAliases(registryPath),
	}
}

// parseSecrets reads the flat secrets file into a name/value map. Non-string
// values (the leading `version: 1`) are ignored. A missing or malformed file
// yields an empty map.
func parseSecrets(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var raw map[string]any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return out
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// registryFile mirrors the parts of the registry this package needs.
type registryFile struct {
	Entries map[string]struct {
		Aliases []string `yaml:"aliases"`
	} `yaml:"entries"`
}

// parseRegistryAliases reads the canonical-to-aliases map from the registry. A
// missing file yields an empty map (no alias fallback). A file that fails strict
// YAML parsing falls back to a line scan, so one malformed line in this shared,
// concurrently edited file costs at most its own entry's aliases instead of
// every alias.
func parseRegistryAliases(path string) map[string][]string {
	out := map[string][]string{}
	if path == "" {
		return out
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var rf registryFile
	if err := yaml.Unmarshal(b, &rf); err == nil {
		for name, entry := range rf.Entries {
			if len(entry.Aliases) > 0 {
				out[name] = entry.Aliases
			}
		}
		return out
	}
	return scanRegistryAliases(b)
}

// aliasLineRe recovers "NAME: {... aliases: [a, b, ...] ...}" from a single
// registry line. The name is UPPER_SNAKE_CASE; the aliases are the bracketed,
// comma-separated list that follows the aliases key on the same line.
var aliasLineRe = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9_]*)\s*:.*\baliases:\s*\[([^\]]*)\]`)

// scanRegistryAliases extracts aliases line by line when the registry does not
// parse as strict YAML. A line without an aliases list contributes nothing, so a
// malformed line simply yields no aliases for its entry rather than aborting the
// whole file.
func scanRegistryAliases(b []byte) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(string(b), "\n") {
		m := aliasLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var aliases []string
		for _, part := range strings.Split(m[2], ",") {
			a := strings.TrimSpace(part)
			a = strings.Trim(a, `"'`)
			if a != "" {
				aliases = append(aliases, a)
			}
		}
		if len(aliases) > 0 {
			out[m[1]] = aliases
		}
	}
	return out
}

// defaultPaths resolves the secrets and registry file paths, honoring the
// environment overrides and otherwise falling back to ~/.config/bugbounty.
func defaultPaths() (secretsPath, registryPath string) {
	dir := strings.TrimSpace(os.Getenv(EnvDir))
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dir = filepath.Join(home, ".config", "bugbounty")
		}
	}
	secretsPath = strings.TrimSpace(os.Getenv(EnvFile))
	if secretsPath == "" && dir != "" {
		secretsPath = filepath.Join(dir, secretsBasename)
	}
	registryPath = strings.TrimSpace(os.Getenv(EnvRegistry))
	if registryPath == "" && dir != "" {
		registryPath = filepath.Join(dir, registryBasename)
	}
	return secretsPath, registryPath
}

var (
	defMu    sync.Mutex
	defStore *Store
	defKey   string
)

// defaultStore returns the process-wide Store, reloading it only when the
// resolved paths or either file's modification time change. Resolving and
// stat-ing on each call is cheap; the files are parsed again only when they
// actually change, so a long-running server picks up an edited secrets file on
// its next lookup without a restart, yet a hot path (notification delivery)
// pays only two stat calls.
func defaultStore() *Store {
	sp, rp := defaultPaths()
	key := fmt.Sprintf("%s|%d|%s|%d", sp, modNano(sp), rp, modNano(rp))
	defMu.Lock()
	defer defMu.Unlock()
	if defStore != nil && defKey == key {
		return defStore
	}
	defStore = LoadFrom(sp, rp)
	defKey = key
	return defStore
}

// modNano returns a file's modification time in nanoseconds, or 0 when it cannot
// be stat-ed (missing or unreadable). A missing file that later appears changes
// this value and triggers a reload.
func modNano(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.ModTime().UnixNano()
}

// Get resolves a canonical name against the process-wide shared files.
func Get(name string) string { return defaultStore().Get(name) }

// Value returns the environment override when envName is set and non-empty, and
// otherwise the shared-file value for the canonical name. It expresses the
// standard precedence in one call: an explicit environment variable wins, the
// shared file is the fallback, and an unconfigured secret resolves to "".
func Value(envName, canonical string) string {
	if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
		return v
	}
	return Get(canonical)
}

// refPattern matches a configuration value that is nothing but a reference of
// the form ${NAME}, where NAME is an UPPER_SNAKE_CASE registry name.
var refPattern = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]*)\}$`)

// ExpandString resolves a whole-value ${NAME} reference to its shared-secret
// value. A string that is not a bare reference, or a reference whose secret has
// no value, is returned unchanged, so a misconfiguration surfaces as a visible
// placeholder rather than silently becoming empty.
func ExpandString(v string) string {
	m := refPattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return v
	}
	if resolved := Get(m[1]); resolved != "" {
		return resolved
	}
	return v
}

// ExpandRefs resolves whole-value ${NAME} references in the top-level string
// values of a configuration map, in place. It is used to keep a secret (such as
// the ARTEX Discord webhook, DISCORD_WEBHOOK_ARTEX) out of persisted channel
// configuration: the stored value is the reference, resolved only at send time.
func ExpandRefs(cfg map[string]any) {
	for k, v := range cfg {
		if s, ok := v.(string); ok {
			if r := ExpandString(s); r != s {
				cfg[k] = r
			}
		}
	}
}
