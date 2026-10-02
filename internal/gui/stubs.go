//go:build gui

package gui

// Auth is the facade service for login, logout, and the auth prompts. It
// binds no methods yet; binding it fixes the frontend's service layout.
type Auth struct{}

// Transfers is the facade service over the Transfer Manager. It binds no
// methods yet.
type Transfers struct{}
