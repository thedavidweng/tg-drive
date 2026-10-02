package service

import (
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
)

// ConfigGetOptions selects what GetConfig reads.
type ConfigGetOptions struct {
	// Key reads one dotted config key; empty reads every key.
	Key string
	// ShowSecrets reveals secret values. It is refused unless Confirmed.
	ShowSecrets bool
	Confirmed   bool
}

// ConfigEntry is one config value as a front end may display it.
type ConfigEntry struct {
	Key string
	// Value is redacted when Secret and secrets were not shown.
	Value  any
	Secret bool
}

// ConfigView is the config read by GetConfig, in config-file key order.
type ConfigView struct {
	ConfigPath string
	Entries    []ConfigEntry
}

// GetConfig reads config values named by opts, redacting secrets unless the
// caller confirmed showing them.
func GetConfig(opts Options, get ConfigGetOptions) (*ConfigView, error) {
	cfg, path, err := LoadConfig(opts)
	if err != nil {
		return nil, err
	}
	if get.ShowSecrets && !get.Confirmed {
		return nil, apperr.New(apperr.ErrConfirmationRequired, "--show-secrets requires --confirm when not running interactively")
	}
	keys := config.Keys
	if get.Key != "" {
		keys = []string{get.Key}
	}
	view := &ConfigView{ConfigPath: path, Entries: make([]ConfigEntry, 0, len(keys))}
	for _, k := range keys {
		v, err := config.GetValue(cfg, k)
		if err != nil {
			return nil, err
		}
		view.Entries = append(view.Entries, ConfigEntry{
			Key:    k,
			Value:  config.RedactValue(k, v, get.ShowSecrets),
			Secret: config.IsSecret(k),
		})
	}
	return view, nil
}

// ConfigSetResult reports a saved config change.
type ConfigSetResult struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

// SetConfig validates and saves one dotted config key.
func SetConfig(opts Options, key, value string) (*ConfigSetResult, error) {
	cfg, path, err := LoadConfig(opts)
	if err != nil {
		return nil, err
	}
	if err := config.SetValue(&cfg, key, value); err != nil {
		return nil, err
	}
	if err := config.Save(path, cfg); err != nil {
		return nil, err
	}
	return &ConfigSetResult{Key: key, Status: "set"}, nil
}
