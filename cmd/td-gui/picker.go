//go:build gui

package main

import (
	"context"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/thedavidweng/tg-drive/internal/gui"
)

// newPicker connects the facade's file dialogs. TD_GUI_PICK_FILES
// (PathListSeparator-joined) and TD_GUI_PICK_DIR script the answers: the
// native dialogs are no-ops in the server build the UI preview runs, so
// the preview sets them to stage picker-driven scenes. Without them the
// Wails dialog manager answers.
func newPicker(app *application.App) gui.FilePicker {
	files, filesOK := os.LookupEnv("TD_GUI_PICK_FILES")
	dir, dirOK := os.LookupEnv("TD_GUI_PICK_DIR")
	if filesOK || dirOK {
		return scriptedPicker{files: files, dir: dir}
	}
	return dialogPicker{app: app}
}

// dialogPicker answers with the platform's native open dialogs.
type dialogPicker struct {
	app *application.App
}

func (p dialogPicker) PickFiles(context.Context) ([]string, error) {
	return p.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseFiles: true,
	}).PromptForMultipleSelection()
}

func (p dialogPicker) PickDirectory(context.Context) (string, error) {
	return p.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseFiles:       false,
		CanChooseDirectories: true,
		CanCreateDirectories: true,
	}).PromptForSingleSelection()
}

// scriptedPicker answers from the TD_GUI_PICK_* environment, for the
// server-mode UI preview.
type scriptedPicker struct {
	files string
	dir   string
}

func (p scriptedPicker) PickFiles(context.Context) ([]string, error) {
	if p.files == "" {
		return nil, nil
	}
	return strings.Split(p.files, string(os.PathListSeparator)), nil
}

func (p scriptedPicker) PickDirectory(context.Context) (string, error) {
	return p.dir, nil
}
