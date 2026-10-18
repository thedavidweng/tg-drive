package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func TestChannelIdentityWinsOverNumericTitle(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	second, err := app.InitRoot(ctx, t.TempDir(), "", "Second", "")
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprint(second.ChannelID)
	if _, err := app.DB.Raw().Exec(`update channels set title=? where title='Test Channel'`, id); err != nil {
		t.Fatal(err)
	}

	selected := WithChannel(ctx, id)
	got, err := app.ChannelTelegramID(selected)
	if err != nil || got != id {
		t.Fatalf("selected channel = %q, %v; want ID %s", got, err, id)
	}
	if err := app.Mkdir(selected, "/only-second"); err != nil {
		t.Fatal(err)
	}
	var actual string
	if err := app.DB.Raw().QueryRow(`select c.tg_channel_id from nodes n
		join channels c on c.id=n.channel_id where n.canonical_path='/only-second'`).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if actual != id {
		t.Fatalf("directory written to channel %s, want %s", actual, id)
	}
}

func TestChannelDuplicateTitleRequiresIdentity(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	second, err := app.InitRoot(ctx, t.TempDir(), "", "Second", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Raw().Exec(`update channels set title='Same'`); err != nil {
		t.Fatal(err)
	}
	err = app.Mkdir(WithChannel(ctx, "Same"), "/ambiguous")
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.ErrUsage || !strings.Contains(ae.Message, "Telegram ID") {
		t.Fatalf("ambiguous title: want ERR_USAGE with identity guidance, got %v", err)
	}
	var count int
	if err := app.DB.Raw().QueryRow(`select count(*) from nodes where canonical_path='/ambiguous'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("ambiguous selection wrote %d directory nodes", count)
	}
	id := fmt.Sprint(second.ChannelID)
	if err := app.Mkdir(WithChannel(ctx, id), "/by-id"); err != nil {
		t.Fatalf("explicit identity must still work for duplicate titles: %v", err)
	}
}
