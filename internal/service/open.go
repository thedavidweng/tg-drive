package service

import (
	"os"
	"strconv"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/telegramgotd"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/core/telegram/fake"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
)

// Options configures Open. Empty paths resolve through the TD_* environment
// and the platform defaults, as config.Load does.
type Options struct {
	ConfigPath  string
	DBPath      string
	SessionPath string
	// Channel optionally selects a configured channel by title or Telegram ID.
	Channel string
	// Wait overrides rate_limit.default_wait for safe flood waits when set.
	Wait *bool
	// Offline opens config and the database only; the App has no Telegram
	// client. For local-only work such as path-codec checks.
	Offline bool
	// Debugf receives diagnostics when set. Messages carry no secrets; paths
	// go through config.DisplayPath.
	Debugf func(format string, args ...any)
}

func (o Options) debugf(format string, args ...any) {
	if o.Debugf != nil {
		o.Debugf(format, args...)
	}
}

// LoadConfig resolves and loads the config named by opts without opening
// the database or Telegram.
func LoadConfig(opts Options) (config.Config, string, error) {
	cfg, path, err := config.Load(config.Overrides{
		ConfigPath:  opts.ConfigPath,
		DBPath:      opts.DBPath,
		SessionPath: opts.SessionPath,
		Channel:     opts.Channel,
	})
	if opts.Debugf != nil {
		state := "found"
		if _, statErr := os.Stat(path); statErr != nil {
			state = "missing, using defaults"
		}
		opts.debugf("config %s (%s)", config.DisplayPath(path), state)
		opts.debugf("db %s", config.DisplayPath(cfg.Storage.DBPath))
		opts.debugf("session %s", config.DisplayPath(cfg.Storage.SessionPath))
		if opts.Channel != "" {
			opts.debugf("channel selector %q", opts.Channel)
		}
	}
	return cfg, path, err
}

// Open is the composition root every front end uses: it loads config, opens
// the database and the Telegram client (the offline fake when
// TD_FAKE_TELEGRAM=1), and returns the App with a function that closes them.
func Open(opts Options) (*App, func(), error) {
	cfg, cfgPath, err := LoadConfig(opts)
	if err != nil {
		return nil, func() {}, err
	}
	database, err := sqlitestore.Open(cfg.Storage.DBPath)
	if err != nil {
		return nil, func() {}, err
	}
	var tg telegram.Client
	if !opts.Offline {
		tg, err = openTelegram(opts, cfg, database)
		if err != nil {
			_ = database.Close()
			return nil, func() {}, err
		}
	}
	app := &App{
		Cfg:        cfg,
		ConfigPath: cfgPath,
		DB:         database,
		TG:         tg,
		Channel:    opts.Channel,
	}
	closeApp := func() {
		if closer, ok := tg.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		_ = database.Close()
	}
	return app, closeApp, nil
}

// Compile-time check: the in-memory fake implements telegram.Client.
var _ telegram.Client = fake.New()

func openTelegram(opts Options, cfg config.Config, database *sqlitestore.DB) (telegram.Client, error) {
	if os.Getenv("TD_FAKE_TELEGRAM") == "1" {
		opts.debugf("telegram: offline fake client (TD_FAKE_TELEGRAM=1)")
		if p := os.Getenv("TD_FAKE_TELEGRAM_STATE"); p != "" {
			return fake.NewPersistent(p), nil
		}
		return fake.New(), nil
	}
	if cfg.Telegram.APIID == 0 || cfg.Telegram.APIHash == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram API credentials missing; run: td auth setup")
	}
	if err := config.EnsureSessionDir(cfg.Storage.SessionPath); err != nil {
		return nil, err
	}
	wait := cfg.RateLimit.DefaultWait
	if opts.Wait != nil {
		wait = *opts.Wait
	}
	client := telegramgotd.New(cfg.Telegram.APIID, cfg.Telegram.APIHash, cfg.Storage.SessionPath,
		wait, time.Duration(cfg.RateLimit.MaxWaitSeconds)*time.Second)
	if opts.Debugf != nil {
		client.SetLogger(opts.Debugf)
		opts.debugf("telegram: flood wait=%t max_wait=%ds", wait, cfg.RateLimit.MaxWaitSeconds)
	}
	registerCachedChannels(client, database)
	return client, nil
}

// registerCachedChannels seeds the client's peer cache with the access
// hashes stored by earlier runs, so channel RPCs work without a dialog scan.
func registerCachedChannels(client *telegramgotd.Client, database *sqlitestore.DB) {
	rows, err := database.Raw().Query(`select tg_channel_id, access_hash, title from channels where access_hash is not null and access_hash != ''`)
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tgID, hash, title string
		if err := rows.Scan(&tgID, &hash, &title); err != nil {
			continue
		}
		chID, err1 := strconv.ParseInt(tgID, 10, 64)
		accHash, err2 := strconv.ParseInt(hash, 10, 64)
		if err1 == nil && err2 == nil {
			client.RegisterChannelInfo(chID, accHash, title)
		}
	}
}
