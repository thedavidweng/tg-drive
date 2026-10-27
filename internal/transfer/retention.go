package transfer

import (
	"context"
	"time"
)

// retention is how long a terminal Transfer stays in the index after it
// ends.
const retention = 30 * 24 * time.Hour

// pruneFinished deletes the terminal Transfers that ended before cutoff.
// It runs when a Manager starts. Every terminal stage, and only those,
// carries a finish time, so the store selects them by it: selecting and
// deleting in one statement means a Transfer a concurrent retry has just
// claimed — which has no finish time — is never removed.
func (m *Manager) pruneFinished(ctx context.Context, cutoff time.Time) error {
	_, err := m.app.DB.DeleteFinishedTransfers(ctx, formatTime(cutoff))
	return err
}

// ClearFinished removes every ended Transfer from the index, from any front
// end, and returns their ids. Active Transfers — a retry that just claimed
// an ended one included — stay, for the same reason.
func (m *Manager) ClearFinished(ctx context.Context) ([]string, error) {
	return m.app.DB.DeleteFinishedTransfers(ctx, "")
}
