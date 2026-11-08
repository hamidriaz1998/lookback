; Extra steps of the MyGo NSIS installer (SPEC "Windows phase", Installer; mygo.json windows.installerInclude).
;
; MyGo includes this file after MUI2.nsh and the pages it declares (Confirm and Instfiles), so a custom
; uninstaller page from here comes after "Uninstallation Complete". The data page therefore deletes the data when
; the user leaves it, not during the uninstall. A pre-uninstall page would need a MyGo hook (docs/followups.md).

!include "LogicLib.nsh"
!include "nsDialogs.nsh"

Var un.DeleteDataBox ; the checkbox control

UninstPage custom un.DataPageShow un.DataPageLeave

; The page offers to delete the default data root. Silent uninstalls (/S) skip pages, so they keep the data.
Function un.DataPageShow
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}
  ${NSD_CreateLabel} 0 0 100% 36u "Lookback is uninstalled. It keeps its index and the downloaded models in:$\r$\n$LOCALAPPDATA\Lookback"
  Pop $0
  ${NSD_CreateCheckbox} 0 42u 100% 12u "Also delete the index and the downloaded models"
  Pop $un.DeleteDataBox
  ${NSD_CreateLabel} 0 60u 100% 24u "If you moved the data folder, the moved folder is kept."
  Pop $0
  nsDialogs::Show
FunctionEnd

; Deletes the default data root when the box is ticked. The app and its server were stopped when the uninstall began.
Function un.DataPageLeave
  ${NSD_GetState} $un.DeleteDataBox $0
  ${If} $0 == ${BST_CHECKED}
    RMDir /r "$LOCALAPPDATA\Lookback"
  ${EndIf}
FunctionEnd

!macro customUnInstall
  ; The uninstaller closed Lookback.exe, and its llama-server.exe dies with it (job object). This catches a stray
  ; one, but only a server that runs from Lookback's own runtimes folder: people who install Lookback may run
  ; llama-server for other tools, and those must not be touched.
  ; The uninstaller is a 32-bit program, and 32-bit PowerShell cannot read the path of a 64-bit process, so use the
  ; 64-bit PowerShell through Sysnative when it exists.
  StrCpy $0 "$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe"
  ${IfNot} ${FileExists} $0
    StrCpy $0 "powershell"
  ${EndIf}
  nsExec::Exec `"$0" -NoProfile -NonInteractive -Command "Get-Process llama-server -ErrorAction SilentlyContinue | Where-Object { $$_.Path -like '$LOCALAPPDATA\Lookback\runtimes\*' } | Stop-Process -Force"`
  Pop $0
  ; Start at login (SPEC section 13): the value Lookback under HKCU\...\Run.
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "Lookback"
!macroend
