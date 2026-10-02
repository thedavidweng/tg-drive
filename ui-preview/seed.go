//go:build ignore

// Command seed adds the fixtures the Import and Maintenance preview scenes
// need to a fake Telegram state file: Saved Messages (a video, a photo, a
// text note — the CLI has no command that creates saved messages) and one
// unmanaged document posted straight to the drive channel for adopt. Run
// after the CLI seeding in run.sh:
//
//	go run ui-preview/seed.go <fake-state.json>
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/core/telegram/fake"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ui-preview/seed.go <fake-state.json>")
		os.Exit(2)
	}
	tg := fake.NewPersistent(os.Args[1])

	origin := &telegram.ForwardOrigin{
		FromID: 777000,
		Title:  "Origin Channel",
		PostID: 4471,
		Date:   time.Date(2024, 3, 11, 10, 22, 0, 0, time.UTC),
	}
	video := []byte("preview placeholder video bytes")
	tg.AddSavedMessage(telegram.Message{
		Kind:     telegram.KindDocument,
		FileName: "kyoto-trip.mp4",
		MIME:     "video/mp4",
		FileSize: int64(len(video)),
		Data:     video,
		Caption:  "Kyoto trip",
		Video: &telegram.VideoAttributes{
			DurationSeconds:   73,
			Width:             1920,
			Height:            1080,
			SupportsStreaming: true,
		},
		Forward:        origin,
		SavedPeerID:    5150,
		SavedPeerTitle: "Trips",
		Date:           time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	photo := []byte("preview placeholder photo bytes")
	tg.AddSavedMessage(telegram.Message{
		Kind:     telegram.KindPhoto,
		MIME:     "image/jpeg",
		FileSize: int64(len(photo)),
		Data:     photo,
		Forward:  origin,
		Date:     time.Date(2024, 6, 2, 9, 30, 0, 0, time.UTC),
	})
	tg.AddSavedMessage(telegram.Message{
		Kind:    telegram.KindText,
		Text:    "museum tickets are in the wallet chat",
		Forward: origin,
		Date:    time.Date(2024, 6, 3, 18, 45, 0, 0, time.UTC),
	})

	// One message posted before td managed the channel, for the adopt
	// preview.
	ch, err := tg.ResolveChannel(context.Background(), "Drive")
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed: resolve Drive channel:", err)
		os.Exit(1)
	}
	backup := []byte("pre-td backup bytes")
	tg.AddMessage(ch.ID, telegram.Message{
		Kind:     telegram.KindDocument,
		FileName: "pre-td-backup.zip",
		MIME:     "application/zip",
		FileSize: int64(len(backup)),
		Data:     backup,
		Date:     time.Date(2023, 11, 20, 8, 0, 0, 0, time.UTC),
	})
}
