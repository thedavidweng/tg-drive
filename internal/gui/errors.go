//go:build gui

package gui

import apperr "github.com/thedavidweng/tg-drive-cli/core/errors"

// Error is the error every facade method returns. Its code and category are
// those of the JSON contract's error envelope, so the frontend handles a
// failure the way a script handles `td --json`.
type Error struct {
	Code     string `json:"code"`
	Category string `json:"category"`
	Message  string `json:"message"`
	// Details carries the envelope's machine-readable extras, such as
	// retry_after_seconds and retry_at on ERR_TELEGRAM_RATE_LIMITED.
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func toError(err error) error {
	if err == nil {
		return nil
	}
	ae, ok := apperr.As(err)
	if !ok {
		// Uncategorized errors map as the CLI's JSON output maps them.
		ae = apperr.New("ERR_UNKNOWN", err.Error())
	}
	e := &Error{Code: ae.Code, Category: string(ae.Category), Message: ae.Message}
	if len(ae.Details) > 0 {
		e.Details = ae.Details
	}
	return e
}
