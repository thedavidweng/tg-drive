package service

import (
	"strconv"
	"strings"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/internal/config"
)

// TelegramField names one Telegram credential a front end may be asked for.
type TelegramField string

const (
	TelegramAPIID   TelegramField = "api_id"
	TelegramAPIHash TelegramField = "api_hash"
	TelegramPhone   TelegramField = "phone"
)

// TelegramAsk supplies the value of a missing credential. Returning an error
// aborts setup without saving.
type TelegramAsk func(TelegramField) (string, error)

// ConfigureTelegram loads the config named by opts, asks for each missing
// credential in the order api_id, api_hash, then phone (only when needPhone),
// and saves the result.
func ConfigureTelegram(opts Options, needPhone bool, ask TelegramAsk) (config.Config, string, error) {
	cfg, path, err := LoadConfig(opts)
	if err != nil {
		return cfg, path, err
	}
	answer := func(f TelegramField) (string, error) {
		s, err := ask(f)
		return strings.TrimSpace(s), err
	}
	if cfg.Telegram.APIID == 0 {
		s, err := answer(TelegramAPIID)
		if err != nil {
			return cfg, path, err
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return cfg, path, apperr.New(apperr.ErrConfigInvalid, "invalid api_id")
		}
		cfg.Telegram.APIID = id
	}
	if cfg.Telegram.APIHash == "" {
		if cfg.Telegram.APIHash, err = answer(TelegramAPIHash); err != nil {
			return cfg, path, err
		}
	}
	if needPhone && cfg.Telegram.Phone == "" {
		if cfg.Telegram.Phone, err = answer(TelegramPhone); err != nil {
			return cfg, path, err
		}
	}
	return cfg, path, config.Save(path, cfg)
}

// TelegramSetupResult reports where setup saved the credentials and where
// the first login will keep its session and index.
type TelegramSetupResult struct {
	APIID       int64  `json:"api_id"`
	ConfigPath  string `json:"config_path"`
	DBPath      string `json:"db_path"`
	SessionPath string `json:"session_path"`
	Status      string `json:"status"`
}

// SetupTelegram saves the API credentials (api_id and api_hash, asked for
// when missing) and creates the session directory and the database, so the
// first login does not fail on a missing data directory.
func SetupTelegram(opts Options, ask TelegramAsk) (*TelegramSetupResult, error) {
	cfg, path, err := ConfigureTelegram(opts, false, ask)
	if err != nil {
		return nil, err
	}
	if err := config.EnsureSessionDir(cfg.Storage.SessionPath); err != nil {
		return nil, err
	}
	opts.Offline = true
	_, closeApp, err := Open(opts)
	if err != nil {
		return nil, err
	}
	closeApp()
	return &TelegramSetupResult{
		APIID:       cfg.Telegram.APIID,
		ConfigPath:  path,
		DBPath:      cfg.Storage.DBPath,
		SessionPath: cfg.Storage.SessionPath,
		Status:      "configured",
	}, nil
}
