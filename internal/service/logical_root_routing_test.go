package service

import (
	"context"
	"fmt"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func TestLogicalRootRoutesOperationsToItsBoundChannel(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	secondRoot := t.TempDir()
	second, err := app.InitRoot(ctx, secondRoot, "", "Second", "")
	if err != nil {
		t.Fatal(err)
	}

	selected := WithLocalRoot(ctx, second.LocalRoot)
	got, err := app.ChannelTelegramID(selected)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprint(second.ChannelID); got != want {
		t.Fatalf("logical root selected channel %q, want %q", got, want)
	}
	if err := app.Mkdir(selected, "/only-second"); err != nil {
		t.Fatal(err)
	}

	var channelID string
	if err := app.DB.Raw().QueryRow(`select c.tg_channel_id from nodes n
		join channels c on c.id=n.channel_id where n.canonical_path='/only-second'`).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprint(second.ChannelID); channelID != want {
		t.Fatalf("logical-root operation wrote to channel %q, want %q", channelID, want)
	}
}

func TestLogicalRootRoutingRejectsUnknownRootBeforeWriting(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)

	err := app.Mkdir(WithLocalRoot(context.Background(), t.TempDir()), "/not-written")
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.ErrChannelNotFound {
		t.Fatalf("unknown logical root: want ERR_CHANNEL_NOT_FOUND, got %v", err)
	}
	var count int
	if err := app.DB.Raw().QueryRow(`select count(*) from nodes where canonical_path='/not-written'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unknown logical root wrote %d nodes", count)
	}
}
