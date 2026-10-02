package transfer

import (
	"context"
	"time"
)

// retention is how long a terminal Transfer stays in the index after it
// ends.
const retention = 30 * 24 * time.Hour

// pruneFinished deletes the terminal Transfers that ended before cutoff.
// It runs when a Manager starts. The predicate is Stage.Terminal, so every
// terminal stage is covered, including ones added after this rule.
func (m *Manager) pruneFinished(ctx context.Context, cutoff time.Time) error {
	rows, err := m.app.DB.ListTransfers(ctx, nil)
	if err != nil {
		return err
	}
	var ids []string
	for _, r := range rows {
		if t := fromRow(r); t.Stage.Terminal() && t.FinishedAt != nil && t.FinishedAt.Before(cutoff) {
			ids = append(ids, t.ID)
		}
	}
	return m.app.DB.DeleteTransfers(ctx, ids)
}
