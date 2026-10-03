package service

import (
	"os"
	"reflect"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func configuredOptions(t *testing.T) Options {
	t.Helper()
	clearTelegramEnv(t)
	opts := openOptions(t)
	cfg := "[telegram]\napi_id = 12345\napi_hash = \"deadbeef\"\nphone = \"+15551234567\"\n"
	if err := os.WriteFile(opts.ConfigPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return opts
}

func entryByKey(t *testing.T, view *ConfigView, key string) ConfigEntry {
	t.Helper()
	for _, e := range view.Entries {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("no %s in %+v", key, view.Entries)
	return ConfigEntry{}
}

// Secrets are redacted unless the caller both asks to see them and has
// confirmed it; a front end learns which entries are secret.
func TestGetConfigRedactsSecrets(t *testing.T) {
	opts := configuredOptions(t)

	view, err := GetConfig(opts, ConfigGetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if view.ConfigPath != opts.ConfigPath || view.Entries[0].Key != "telegram.api_id" {
		t.Fatalf("view = %s %+v", view.ConfigPath, view.Entries)
	}
	for key, want := range map[string]ConfigEntry{
		"telegram.api_id":   {Key: "telegram.api_id", Value: int64(12345)},
		"telegram.api_hash": {Key: "telegram.api_hash", Value: "redacted", Secret: true},
		"telegram.phone":    {Key: "telegram.phone", Value: "+1********67", Secret: true},
		"upload.threads":    {Key: "upload.threads", Value: 4},
	} {
		if got := entryByKey(t, view, key); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %+v, want %+v", key, got, want)
		}
	}

	_, err = GetConfig(opts, ConfigGetOptions{ShowSecrets: true})
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrConfirmationRequired {
		t.Fatalf("unconfirmed show secrets: err = %v", err)
	}

	view, err = GetConfig(opts, ConfigGetOptions{Key: "telegram.api_hash", ShowSecrets: true, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []ConfigEntry{{Key: "telegram.api_hash", Value: "deadbeef", Secret: true}}; !reflect.DeepEqual(view.Entries, want) {
		t.Fatalf("confirmed key = %+v, want %+v", view.Entries, want)
	}

	_, err = GetConfig(opts, ConfigGetOptions{Key: "no.such.key"})
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrUsage {
		t.Fatalf("unknown key: err = %v", err)
	}
}

// A set value is saved and read back; an invalid one changes nothing.
func TestSetConfigPersists(t *testing.T) {
	opts := configuredOptions(t)

	got, err := SetConfig(opts, "upload.threads", "3")
	if err != nil {
		t.Fatal(err)
	}
	if want := (&ConfigSetResult{Key: "upload.threads", Status: "set"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("set = %+v, want %+v", got, want)
	}
	if _, err := SetConfig(opts, "upload.threads", "0"); err == nil {
		t.Fatal("upload.threads=0 accepted")
	} else if ae, ok := apperr.As(err); !ok || ae.Code != apperr.ErrConfigInvalid {
		t.Fatalf("invalid value: err = %v", err)
	}
	view, err := GetConfig(opts, ConfigGetOptions{Key: "upload.threads"})
	if err != nil {
		t.Fatal(err)
	}
	if v := view.Entries[0].Value; v != 3 {
		t.Fatalf("upload.threads = %v, want 3", v)
	}
}
