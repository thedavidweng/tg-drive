; NSIS installer for td-gui. Self-contained: unlike the Wails project.nsi it
; does not include the generated wails_tools.nsh. Version, binary, WebView2
; bootstrapper and output file arrive as -D defines from
; build/windows/package.sh:
;
;   makensis -DVERSION=1.2.3 -DBINARY=dist/.../td-gui.exe \
;     -DBOOTSTRAPPER=dist/.../MicrosoftEdgeWebview2Setup.exe \
;     -DOUTFILE=dist/gui/td-gui_1.2.3_windows_x86_64-installer.exe \
;     build/windows/td-gui.nsi
Unicode true

!ifndef VERSION
	!define VERSION "0.0.0"
!endif
!ifndef BINARY
	!error "BINARY is required: path to td-gui.exe"
!endif
!ifndef BOOTSTRAPPER
	!error "BOOTSTRAPPER is required: path to MicrosoftEdgeWebview2Setup.exe"
!endif
!ifndef OUTFILE
	!define OUTFILE "td-gui_${VERSION}-installer.exe"
!endif

!define PRODUCT_NAME "td"
!define PRODUCT_BINARY "td-gui.exe"
!define PRODUCT_PUBLISHER "thedavidweng"
!define PRODUCT_URL "https://github.com/thedavidweng/tg-drive-cli"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\td-gui"
; WebView2 evergreen runtime client ID used by EdgeUpdate.
!define WEBVIEW2_CLIENT "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

Name "${PRODUCT_NAME}"
OutFile "${OUTFILE}"
; Per-user install: no UAC prompt for an unsigned installer.
InstallDir "$LOCALAPPDATA\Programs\td-gui"
RequestExecutionLevel user
ShowInstDetails show
ManifestDPIAware true

VIProductVersion "${VERSION}.0"
VIAddVersionKey "CompanyName" "${PRODUCT_PUBLISHER}"
VIAddVersionKey "FileDescription" "td-gui installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "ProductName" "${PRODUCT_NAME}"
VIAddVersionKey "LegalCopyright" "Apache-2.0"

!include "MUI2.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!include "FileFunc.nsh"

!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
	${IfNot} ${AtLeastWin10}
		IfSilent 0 +3
		SetErrorLevel 64
		Abort
		MessageBox MB_OK "td-gui is only supported on Windows 10 and later."
		Quit
	${EndIf}
	${IfNot} ${IsNativeAMD64}
		IfSilent 0 +3
		SetErrorLevel 65
		Abort
		MessageBox MB_OK "This installer supports 64-bit x86 Windows only."
		Quit
	${EndIf}
FunctionEnd

Section "Install"
	SetOutPath "$INSTDIR"
	File "/oname=${PRODUCT_BINARY}" "${BINARY}"

	; Install the WebView2 runtime only when EdgeUpdate reports none.
	SetRegView 64
	ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_CLIENT}" "pv"
	${If} $0 == ""
		ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_CLIENT}" "pv"
	${EndIf}
	${If} $0 == ""
		DetailPrint "Installing: WebView2 Runtime"
		InitPluginsDir
		CreateDirectory "$pluginsdir\webview2bootstrapper"
		SetOutPath "$pluginsdir\webview2bootstrapper"
		File "${BOOTSTRAPPER}"
		ExecWait '"$pluginsdir\webview2bootstrapper\MicrosoftEdgeWebview2Setup.exe" /silent /install'
		SetOutPath "$INSTDIR"
	${EndIf}

	CreateShortcut "$SMPROGRAMS\${PRODUCT_NAME}.lnk" "$INSTDIR\${PRODUCT_BINARY}"
	CreateShortcut "$DESKTOP\${PRODUCT_NAME}.lnk" "$INSTDIR\${PRODUCT_BINARY}"

	WriteUninstaller "$INSTDIR\uninstall.exe"
	WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${PRODUCT_PUBLISHER}"
	WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${PRODUCT_NAME}"
	WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
	WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_BINARY}"
	WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
	WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
	WriteRegStr HKCU "${UNINST_KEY}" "URLInfoAbout" "${PRODUCT_URL}"
	${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
	IntFmt $0 "0x%08X" $0
	WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Section "Uninstall"
	; WebView2's per-user data directory.
	RMDir /r "$LOCALAPPDATA\td-gui"
	RMDir /r "$INSTDIR"
	Delete "$SMPROGRAMS\${PRODUCT_NAME}.lnk"
	Delete "$DESKTOP\${PRODUCT_NAME}.lnk"
	DeleteRegKey HKCU "${UNINST_KEY}"
SectionEnd
