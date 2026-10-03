package service

import (
	"errors"
	"os"
	"reflect"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

// askFrom answers each asked field from answers and records the order asked.
func askFrom(answers map[TelegramField]string, asked *[]TelegramField) TelegramAsk {
	return func(f TelegramField) (string, error) {
		*asked = append(*asked, f)
		return answers[f], nil
	}
}

// Setup asks only for the missing API credentials, never the phone, saves
// them, and prepares the database so the first login finds its storage.
func TestSetupTelegramAsksForMissingCredentials(t *testing.T) {
	clearTelegramEnv(t)
	opts := openOptions(t)
	var asked []TelegramField

	got, err := SetupTelegram(opts, askFrom(map[TelegramField]string{
		TelegramAPIID: " 777 ", TelegramAPIHash: "abc\n",
	}, &asked))
	if err != nil {
		t.Fatal(err)
	}
	if want := []TelegramField{TelegramAPIID, TelegramAPIHash}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("asked %v, want %v", asked, want)
	}
	want := &TelegramSetupResult{
		APIID: 777, ConfigPath: opts.ConfigPath, DBPath: opts.DBPath,
		SessionPath: opts.SessionPath, Status: "configured",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("setup = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(opts.DBPath); err != nil {
		t.Fatalf("setup must create the database: %v", err)
	}
	cfg, _, err := LoadConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.APIID != 777 || cfg.Telegram.APIHash != "abc" {
		t.Fatalf("saved credentials = %+v", cfg.Telegram)
	}

	asked = nil
	if _, err := SetupTelegram(opts, askFrom(nil, &asked)); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 {
		t.Fatalf("complete config asked %v", asked)
	}
}

// A malformed api_id fails before anything else is asked or saved.
func TestSetupTelegramRejectsBadAPIID(t *testing.T) {
	clearTelegramEnv(t)
	opts := openOptions(t)
	var asked []TelegramField

	_, err := SetupTelegram(opts, askFrom(map[TelegramField]string{TelegramAPIID: "x"}, &asked))
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrConfigInvalid {
		t.Fatalf("err = %v, want %s", err, apperr.ErrConfigInvalid)
	}
	if !reflect.DeepEqual(asked, []TelegramField{TelegramAPIID}) {
		t.Fatalf("asked %v after a bad api_id", asked)
	}
	if _, err := os.Stat(opts.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("bad api_id wrote config: %v", err)
	}
}

// Login needs the phone too; an answer the front end cannot give aborts
// without saving a half-filled config.
func TestConfigureTelegramForLogin(t *testing.T) {
	clearTelegramEnv(t)
	opts := openOptions(t)
	var asked []TelegramField

	cfg, path, err := ConfigureTelegram(opts, true, askFrom(map[TelegramField]string{
		TelegramAPIID: "777", TelegramAPIHash: "abc", TelegramPhone: "+15550001111",
	}, &asked))
	if err != nil {
		t.Fatal(err)
	}
	if want := []TelegramField{TelegramAPIID, TelegramAPIHash, TelegramPhone}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("asked %v, want %v", asked, want)
	}
	if path != opts.ConfigPath || cfg.Telegram.Phone != "+15550001111" {
		t.Fatalf("configured %s %+v", path, cfg.Telegram)
	}

	other := openOptions(t)
	cancelled := errors.New("cancelled")
	_, _, err = ConfigureTelegram(other, true, func(f TelegramField) (string, error) {
		if f == TelegramPhone {
			return "", cancelled
		}
		return "1", nil
	})
	if !errors.Is(err, cancelled) {
		t.Fatalf("err = %v, want the ask error", err)
	}
	if _, err := os.Stat(other.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("aborted setup wrote config: %v", err)
	}
}
