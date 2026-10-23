Unicode true

!define APPNAME "Aircast"
!define VERSION "0.1.0"
!define PUBLISHER "Aircast contributors"

Name "${APPNAME} ${VERSION}"
OutFile "../dist/aircast-${VERSION}-windows-amd64-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\Aircast"
InstallDirRegKey HKCU "Software\Aircast" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
SetCompressorDictSize 32

VIProductVersion "0.1.0.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "Aircast installer"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "LegalCopyright" "GPL-3.0"

!include "MUI2.nsh"
!define MUI_ICON "aircast.ico"
!define MUI_UNICON "aircast.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\aircast.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open Aircast"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Aircast" SEC_MAIN
  SectionIn RO
  SetOutPath "$INSTDIR"
  File /r "payload/*"
  File "aircast.ico"

  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateDirectory "$SMPROGRAMS\Aircast"
  CreateShortcut "$SMPROGRAMS\Aircast\Aircast.lnk" "$INSTDIR\aircast.exe" "" "$INSTDIR\aircast.ico" 0
  CreateShortcut "$SMPROGRAMS\Aircast\Uninstall Aircast.lnk" "$INSTDIR\uninstall.exe"

  WriteRegStr HKCU "Software\Aircast" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast" "DisplayName" "${APPNAME} ${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast" "Publisher" "${PUBLISHER}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast" "DisplayIcon" "$INSTDIR\aircast.ico"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast" "InstallLocation" "$INSTDIR"
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast" "NoRepair" 1
SectionEnd

Section "Desktop shortcut" SEC_DESKTOP
  CreateShortcut "$DESKTOP\Aircast.lnk" "$INSTDIR\aircast.exe" "" "$INSTDIR\aircast.ico" 0
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SEC_MAIN} "The panel, casting engine, command-line tool and the FFmpeg build they use."
  !insertmacro MUI_DESCRIPTION_TEXT ${SEC_DESKTOP} "Add an Aircast shortcut to the desktop."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  Delete "$SMPROGRAMS\Aircast\Aircast.lnk"
  Delete "$SMPROGRAMS\Aircast\Uninstall Aircast.lnk"
  RMDir "$SMPROGRAMS\Aircast"
  Delete "$DESKTOP\Aircast.lnk"

  RMDir /r "$INSTDIR"

  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Aircast"
  DeleteRegKey HKCU "Software\Aircast"
SectionEnd
