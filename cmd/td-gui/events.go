//go:build gui

package main

// Every typed event is registered in this file and nowhere else. Call
// application.RegisterEvent directly from init with a constant name: the
// binding generator discovers events, and types them for the frontend, only
// from such calls. Event data types belong in internal/gui, which never
// imports Wails. No event is registered yet.
