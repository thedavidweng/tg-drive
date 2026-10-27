//go:build gui

package gui

import (
	"context"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/core/telegram"
	"github.com/thedavidweng/tg-drive/core/telegram/fake"
)

type blockingAuthClient struct {
	telegram.Client
	entered chan struct{}
	release chan struct{}
	exited  chan struct{}
	lateOK  bool
}

func (c *blockingAuthClient) wait(ctx context.Context) error {
	close(c.entered)
	select {
	case <-ctx.Done():
	case <-c.release:
	}
	close(c.exited)
	if c.lateOK {
		return nil
	}
	return ctx.Err()
}

func (c *blockingAuthClient) Status(ctx context.Context) (*telegram.User, bool, error) {
	return nil, false, c.wait(ctx)
}

func (c *blockingAuthClient) Logout(ctx context.Context) error { return c.wait(ctx) }

func TestCloseCancelsActiveAuthCalls(t *testing.T) {
	for _, operation := range []string{"status", "logout"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("TD_FAKE_TELEGRAM", "1")
			dir := t.TempDir()
			t.Setenv("TD_CONFIG", dir+"/config.toml")
			t.Setenv("TD_DB", dir+"/index.db")
			t.Setenv("TD_SESSION", dir+"/session.json")
			svc, closeServices, err := Open()
			if err != nil {
				t.Fatal(err)
			}
			client := &blockingAuthClient{Client: fake.New(), entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{}), lateOK: true}
			svc.state.app.TG = client
			oldClose := svc.state.closeApp
			released := make(chan bool, 1)
			svc.state.closeApp = func() {
				select {
				case <-client.exited:
					released <- true
				default:
					released <- false
				}
				oldClose()
			}
			finished := make(chan error, 1)
			go func() {
				if operation == "status" {
					_, err := svc.Auth.Status(context.Background())
					finished <- err
				} else {
					finished <- svc.Auth.Logout(context.Background())
				}
			}()
			<-client.entered
			closed := make(chan struct{})
			go func() { closeServices(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(time.Second):
				close(client.release)
				<-closed
				t.Fatal("close did not cancel active auth operation")
			}
			if err := <-finished; err == nil {
				t.Fatal("cancelled operation succeeded")
			}
			if !<-released {
				t.Fatal("resources closed before operation unwound")
			}
		})
	}
}

func TestAuthStatusHonorsCallerCancellation(t *testing.T) {
	t.Setenv("TD_FAKE_TELEGRAM", "1")
	dir := t.TempDir()
	t.Setenv("TD_CONFIG", dir+"/config.toml")
	t.Setenv("TD_DB", dir+"/index.db")
	t.Setenv("TD_SESSION", dir+"/session.json")
	svc, closeServices, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer closeServices()
	client := &blockingAuthClient{Client: fake.New(), entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{}), lateOK: true}
	svc.state.app.TG = client
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := svc.Auth.Status(ctx); done <- err }()
	<-client.entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("status returned a late success after caller cancellation")
		}
	case <-time.After(time.Second):
		close(client.release)
		t.Fatal("caller cancellation did not reach status")
	}
}
