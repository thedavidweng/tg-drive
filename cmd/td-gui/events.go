//go:build gui

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/thedavidweng/tg-drive-cli/internal/gui"
)

// Every typed event is registered in this file and nowhere else. Call
// application.RegisterEvent directly from init with a constant name: the
// binding generator discovers events, and types them for the frontend, only
// from such calls. Event data types belong in internal/gui, which never
// imports Wails.
// EventAuthPrompt asks the frontend for a login code or the 2FA password.
const EventAuthPrompt = "auth.prompt"

func init() {
	application.RegisterEvent[gui.OmarchyTheme](gui.OmarchyThemeChangedEvent)
	application.RegisterEvent[gui.AuthPrompt](EventAuthPrompt)
	application.RegisterEvent[gui.DirectoryChanged](gui.EventDirectoryChanged)
	application.RegisterEvent[gui.ScanProgress](gui.EventScanProgress)
	application.RegisterEvent[gui.Transfer](gui.EventTransferStage)
	application.RegisterEvent[gui.Transfer](gui.EventTransferProgress)
	application.RegisterEvent[gui.TransferRemoved](gui.EventTransferRemoved)
	application.RegisterEvent[gui.FilesDropped](gui.EventFilesDropped)
	application.RegisterEvent[gui.ImportPrompt](gui.EventImportPrompt)
	application.RegisterEvent[gui.ItemEvent](gui.EventImportItem)
	application.RegisterEvent[gui.ItemEvent](gui.EventRepairItem)
	application.RegisterEvent[gui.ChannelsChanged](gui.EventChannelsChanged)
}
