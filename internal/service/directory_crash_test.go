package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive/core/telegram/fake"
	"github.com/thedavidweng/tg-drive/internal/config"
)

func directoryCrashApp(t *testing.T, dir string) (*App, *fake.Client) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Locks.TTLSeconds = 1
	cfg.Storage.DBPath = filepath.Join(dir, "index.db")
	db, err := sqlitestore.Open(cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tg := fake.NewPersistent(filepath.Join(dir, "telegram.json"))
	app := &App{Cfg: cfg, DB: db, TG: tg}
	auditWrites(t, app, tg)
	return app, tg
}

func TestDirectoryProcessKillAndRetry(t *testing.T) {
	ctx := context.Background()
	run := func(app *App, operation string) error {
		if operation == "move" {
			return app.MoveFile(ctx, "/batch", "/target", MoveOptions{Confirm: true})
		}
		_, err := app.DeleteFile(ctx, "/batch", DeleteOptions{Confirm: true})
		return err
	}
	// The child blocks before the second remote write. Killing it prevents
	// deferred checkpoints, lock release and database close from running.
	if dir := os.Getenv("TD_DIRECTORY_CRASH_DIR"); dir != "" {
		app, _ := directoryCrashApp(t, dir)
		channel, _, err := app.channelID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		row, found, err := app.DB.ActiveByPath(ctx, channel, "/batch/b.txt")
		if err != nil || !found {
			t.Fatalf("second file: %+v, %v", row, err)
		}
		app.TG = &interruptDirectoryClient{
			Client: app.TG, messageID: int(row.MessageID.Int64),
			interrupt: func() error {
				if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0o600); err != nil {
					return err
				}
				<-ctx.Done()
				return ctx.Err()
			},
		}
		if err := run(app, os.Getenv("TD_DIRECTORY_CRASH_OPERATION")); err != nil {
			t.Fatal(err)
		}
		t.Fatal("worker unexpectedly completed")
	}

	for _, operation := range []string{"move", "delete"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			app, tg := directoryCrashApp(t, dir)
			loginAndInit(t, app, tg)
			for _, path := range []string{"/batch/a.txt", "/batch/b.txt", "/batch/c.txt", "/target/keep.txt"} {
				if _, err := app.UploadFile(ctx, writeLocal(t, path), path, ConflictFail, false, UploadOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestDirectoryProcessKillAndRetry$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "TD_DIRECTORY_CRASH_DIR="+dir, "TD_DIRECTORY_CRASH_OPERATION="+operation)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
			for deadline := time.Now().Add(15 * time.Second); ; {
				_, err := os.Stat(filepath.Join(dir, "ready"))
				if err == nil {
					break
				}
				if !os.IsNotExist(err) || time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("worker did not reach second write: %v; %s", err, output.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			killed := time.Now()
			if err := cmd.Wait(); err == nil {
				t.Fatal("worker exited successfully despite Kill")
			}
			if err := app.DB.Close(); err != nil {
				t.Fatal(err)
			}
			app, _ = directoryCrashApp(t, dir)
			table, countColumn := "directory_move_operations", "moved"
			if operation == "delete" {
				table, countColumn = "directory_delete_operations", "deleted"
			}
			type checkpoint struct {
				id, status, lastPath              string
				total, changed, failed, remaining int
			}
			read := func(id string) checkpoint {
				t.Helper()
				var p checkpoint
				query := fmt.Sprintf("select id,status,last_path,total,%s,failed,remaining from %s", countColumn, table)
				var args []any
				if id != "" {
					query += " where id=?"
					args = append(args, id)
				} else {
					query += " order by rowid desc limit 1"
				}
				if err := app.DB.Raw().QueryRow(query, args...).Scan(&p.id, &p.status, &p.lastPath, &p.total, &p.changed, &p.failed, &p.remaining); err != nil {
					t.Fatal(err)
				}
				return p
			}
			interrupted := read("")
			if interrupted.status != "running" || interrupted.total != 3 || interrupted.changed != 1 ||
				interrupted.failed != 0 || interrupted.remaining != 2 || interrupted.lastPath != "/batch/b.txt" {
				t.Fatalf("killed checkpoint = %+v", interrupted)
			}
			// Operation leases compare at second precision, so allow two
			// seconds for a one-second lease before the explicit retry.
			if wait := time.Until(killed.Add(2300 * time.Millisecond)); wait > 0 {
				time.Sleep(wait)
			}
			if err := run(app, operation); err != nil {
				t.Fatal(err)
			}
			retry := read("")
			if retry.id == interrupted.id || retry.status != "completed" || retry.total != 2 ||
				retry.changed != 2 || retry.failed != 0 || retry.remaining != 0 {
				t.Fatalf("retry checkpoint = %+v", retry)
			}
			if got := read(interrupted.id); got != interrupted {
				t.Fatalf("retry changed killed history: %+v, want %+v", got, interrupted)
			}
			for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
				want := "deleted"
				if operation == "move" {
					want = ""
					if fileStatus(t, app, "/target/batch/"+name) != "active" {
						t.Fatalf("retry lost moved file %s", name)
					}
				}
				if got := fileStatus(t, app, "/batch/"+name); got != want {
					t.Fatalf("source %s = %q, want %q", name, got, want)
				}
			}
			if fileStatus(t, app, "/target/keep.txt") != "active" {
				t.Fatal("retry changed unrelated destination file")
			}
			scan, err := app.Scan(ctx, ScanOptions{Full: true})
			wantActive := 1
			if operation == "move" {
				wantActive = 4
			}
			if err != nil || scan.Active != wantActive {
				t.Fatalf("remote reconstruction after retry = %+v, %v; want %d active", scan, err, wantActive)
			}
			if operation == "move" {
				for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
					dest := filepath.Join(dir, name)
					if _, err := app.DownloadFile(ctx, "/target/batch/"+name, dest, ConflictFail, DownloadOptions{}); err != nil {
						t.Fatal(err)
					}
					if got, err := os.ReadFile(dest); err != nil || string(got) != "/batch/"+name {
						t.Fatalf("reconstructed %s bytes = %q, %v", name, got, err)
					}
				}
			}
		})
	}
}
