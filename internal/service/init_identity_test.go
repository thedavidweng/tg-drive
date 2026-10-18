package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

type initResolutionFailure struct {
	telegram.Client
	err     error
	created int
}

func (c *initResolutionFailure) ResolveChannel(context.Context, string) (*telegram.Channel, error) {
	return nil, c.err
}

func (c *initResolutionFailure) CreateChannel(ctx context.Context, title string) (*telegram.Channel, error) {
	c.created++
	return c.Client.CreateChannel(ctx, title)
}

func TestInitDoesNotCreateAfterResolutionFailure(t *testing.T) {
	for _, cause := range []error{
		apperr.New(apperr.ErrUsage, "ambiguous channel title"),
		&telegram.FloodWaitError{Seconds: 30},
		context.Canceled,
		errors.New("network disconnected"),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			probe := &initResolutionFailure{Client: app.TG, err: cause}
			app.TG = probe
			_, err := app.InitRoot(context.Background(), t.TempDir(), "Uncertain", "", "")
			want, _ := apperr.As(telegram.MapError(cause))
			got, ok := apperr.As(err)
			if !ok || got.Code != want.Code || probe.created != 0 {
				t.Fatalf("init = %v, create calls=%d; want %s and no create", err, probe.created, want.Code)
			}
			var bindings int
			if err := app.DB.Raw().QueryRow(`select count(*) from channels`).Scan(&bindings); err != nil {
				t.Fatal(err)
			}
			if bindings != 1 {
				t.Fatalf("resolution failure changed bindings: %d", bindings)
			}
		})
	}
}

func TestInitRemoteDuplicateTitleRequiresIdentity(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	first, err := tg.CreateChannel(ctx, "Same")
	if err != nil {
		t.Fatal(err)
	}
	second, err := tg.CreateChannel(ctx, "Same")
	if err != nil {
		t.Fatal(err)
	}
	for _, bind := range []bool{false, true} {
		title, selector := "Same", ""
		if bind {
			title, selector = "", "Same"
		}
		_, err := app.InitRoot(ctx, t.TempDir(), title, "", selector)
		ae, ok := apperr.As(err)
		if !ok || ae.Code != apperr.ErrUsage {
			t.Fatalf("duplicate title bind=%t: %v; want ERR_USAGE", bind, err)
		}
	}
	for _, ch := range []*telegram.Channel{first, second} {
		result, err := app.InitRoot(ctx, t.TempDir(), "", "", fmt.Sprint(ch.ID))
		if err != nil || result.ChannelID != ch.ID {
			t.Fatalf("explicit remote ID %d = %+v, %v", ch.ID, result, err)
		}
	}
}

func TestInitCreatesOnlyWhenChannelIsAbsent(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	probe := &initResolutionFailure{
		Client: app.TG,
		err:    apperr.New(apperr.ErrChannelNotFound, "channel not found"),
	}
	app.TG = probe
	result, err := app.InitRoot(context.Background(), t.TempDir(), "New", "", "")
	if err != nil || result.ChannelTitle != "New" || probe.created != 1 {
		t.Fatalf("absent channel = %+v, %v, creates=%d", result, err, probe.created)
	}
}
