// Package config handles on-disk state under ~/.bonpreu: the user-authored
// config.toml (session + defaults) and the machine-managed session cache.
// BONPREU_CONFIG_DIR overrides the directory.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const configFile = "config.toml"

// Dir is ~/.bonpreu (or $BONPREU_CONFIG_DIR).
func Dir() string {
	if d := os.Getenv("BONPREU_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".bonpreu"
	}
	return filepath.Join(home, ".bonpreu")
}

// Config mirrors ~/.bonpreu/config.toml.
type Config struct {
	Auth struct {
		Cookie string `toml:"cookie"` // browser session (global_sid + aws-waf-token) for the authenticated leg
	} `toml:"auth"`
	Defaults struct {
		RegionID   string `toml:"region_id"`   // scopes cart/checkout prices; resolved from postal code
		Lang       string `toml:"lang"`        // "ca" (default) or "es"
		PostalCode string `toml:"postal_code"` // human-meaningful source; region is derived from it
	} `toml:"defaults"`
	Limits struct {
		MaxEUR float64 `toml:"max_eur"` // refuse carts/checkouts over this total (0 = no limit)
	} `toml:"limits"`
	Brands Brands `toml:"brands"`
	Offers struct {
		// Mode decides what the shop does with an actionable multi-buy offer
		// (2nd-unit, lot, NxM): "alert" (default — surface it and ask before
		// taking it), "ignore" (don't surface), "accept" (default to taking it).
		Mode string `toml:"mode"`
	} `toml:"offers"`
}

// Brands holds the user's preferred-brand selection. Empty = no preference (the
// resolver keeps picking the cheapest/organic hit). Preferred is a global,
// priority-ordered list; Overrides maps a search term/category to its own ordered
// list (winning over Preferred for matching items).
type Brands struct {
	Preferred []string            `toml:"preferred"`  // global, ordered by priority
	Mode      string              `toml:"mode"`       // "cheapest_among" (default) | "strict_priority"
	OnMissing string              `toml:"on_missing"` // "ask" (default) | "cheapest"
	Overrides map[string][]string `toml:"overrides"`  // term/category → ordered brand list
}

// Configured reports whether any brand preference is set.
func (b Brands) Configured() bool { return len(b.Preferred) > 0 || len(b.Overrides) > 0 }

// LoadConfig reads config.toml. A missing file is not an error (empty config).
// Any other stat error (permission, broken path) IS returned: callers like
// resolveMax rely on a malformed/unreadable config failing rather than silently
// disabling the spending guard, so only os.IsNotExist may be swallowed.
func LoadConfig() (Config, error) {
	var c Config
	p := filepath.Join(Dir(), configFile)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, fmt.Errorf("stat config %s: %w", p, err)
	}
	_, err := toml.DecodeFile(p, &c)
	return c, err
}

// ensureDir creates the config dir (0700; it holds secrets) if absent.
func ensureDir() error {
	return os.MkdirAll(Dir(), 0o700)
}

// Load reads a JSON state file (e.g. the session cache) from the config dir.
func Load(name string, v any) error {
	b, err := os.ReadFile(filepath.Join(Dir(), name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Save writes v as pretty JSON to name in the config dir, 0600 (secrets). The
// write is atomic — a temp file in the same dir, then os.Rename — so a crash or
// disk-full mid-write cannot truncate the credential store (session.json) and
// leave the user with a corrupt, half-written session.
func Save(name string, v any) error {
	if err := ensureDir(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := Dir()
	tmp, err := os.CreateTemp(dir, name+".tmp-*") // CreateTemp opens with 0600
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // cleanup on error; no-op once the rename below succeeds
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}
