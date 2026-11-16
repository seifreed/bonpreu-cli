//go:build !windows

package config

func secureConfigDir(string) error { return nil }
