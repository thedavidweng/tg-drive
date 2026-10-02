package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"time"
)

// pollInterval is how often Watch rereads the index. It matches the
// progress write throttle, so a change lands in the next poll; polling a
// local indexed table this fast costs next to nothing and needs no
// cross-process signalling.
const pollInterval = 250 * time.Millisecond

// Watch follows every Transfer in the index until ctx ends: fn runs with
// the current Transfers (newest first) once at the start and then each
// time any of them changes. Polling the shared index is how Watch sees the
// Transfers of other processes; a Transfer that passes a stage between two
// polls is seen at its later stage.
func (m *Manager) Watch(ctx context.Context, fn func([]Transfer) error) error {
	var prev []byte
	for {
		list, err := m.List(ctx, Filter{All: true})
		if err != nil {
			return err
		}
		cur, _ := json.Marshal(list)
		if !bytes.Equal(cur, prev) {
			if err := fn(list); err != nil {
				return err
			}
			prev = cur
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
