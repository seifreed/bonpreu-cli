package main

import (
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/seifreed/bonpreu-cli/internal/config"
)

// resolveMax returns the spending cap in € from (in order) the --max flag, the
// BONPREU_MAX_EUR env var, or [limits].max_eur in config.toml. 0 = no limit.
func resolveMax(flagMax float64, flagSet bool) (float64, error) {
	if flagSet {
		return validatedMax(flagMax, "--max")
	}
	if s := os.Getenv("BONPREU_MAX_EUR"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid BONPREU_MAX_EUR %q: %v", s, err)
		}
		return validatedMax(v, "BONPREU_MAX_EUR")
	}
	// A malformed config must not silently disable the limit: fail safe so the
	// caller (and the user) sees the spending guard could not be resolved.
	cfg, err := config.LoadConfig()
	if err != nil {
		return 0, fmt.Errorf("read [limits].max_eur from config: %w", err)
	}
	return validatedMax(cfg.Limits.MaxEUR, "[limits].max_eur")
}

func validatedMax(value float64, source string) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("invalid %s (want a finite, non-negative amount)", source)
	}
	return value, nil
}

type budgetExceededError struct {
	total  float64
	maxEUR float64
	action string
}

func (e *budgetExceededError) Error() string {
	return fmt.Sprintf("BUDGET EXCEEDED: %s total %.2f€ is over the %.2f€ limit — refusing (raise with --max, BONPREU_MAX_EUR, or [limits].max_eur)", e.action, e.total, e.maxEUR)
}

// checkBudget fails when a known total exceeds the cap. maxEUR <= 0 disables it.
func checkBudget(total float64, maxEUR float64, action string) error {
	if _, err := validatedMax(maxEUR, "budget limit"); err != nil {
		return err
	}
	if math.IsNaN(total) || math.IsInf(total, 0) || total < 0 {
		return fmt.Errorf("invalid %s total (want a finite, non-negative amount)", action)
	}
	if maxEUR <= 0 {
		return nil
	}
	if total > maxEUR {
		return &budgetExceededError{action: action, total: total, maxEUR: maxEUR}
	}
	return nil
}
