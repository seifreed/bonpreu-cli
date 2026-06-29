package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/seifreed/bonpreu-cli/internal/config"
)

// resolveMax returns the spending cap in € from (in order) the --max flag, the
// BONPREU_MAX_EUR env var, or [limits].max_eur in config.toml. 0 = no limit.
func resolveMax(flagMax float64) (float64, error) {
	if flagMax > 0 {
		return flagMax, nil
	}
	if s := os.Getenv("BONPREU_MAX_EUR"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid BONPREU_MAX_EUR %q: %v", s, err)
		}
		return v, nil
	}
	// A malformed config must not silently disable the limit: fail safe so the
	// caller (and the user) sees the spending guard could not be resolved.
	cfg, err := config.LoadConfig()
	if err != nil {
		return 0, fmt.Errorf("read [limits].max_eur from config: %w", err)
	}
	return cfg.Limits.MaxEUR, nil
}

// checkBudget fails when a known total exceeds the cap. maxEUR <= 0 disables it.
func checkBudget(total float64, maxEUR float64, action string) error {
	if maxEUR <= 0 {
		return nil
	}
	if total > maxEUR {
		return fmt.Errorf("BUDGET EXCEEDED: %s total %.2f€ is over the %.2f€ limit — refusing (raise with --max, BONPREU_MAX_EUR, or [limits].max_eur)", action, total, maxEUR)
	}
	return nil
}
