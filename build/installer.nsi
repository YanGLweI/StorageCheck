; StorageCheck 安装脚本（NSIS 3，可在 macOS/Linux 上用 makensis 直接编译）
; 用法：./build.sh 1.0.0 会先产出 exe 再自动编译本脚本；
; 手动：makensis -DVERSION=1.0.0 -DARCH=amd64 build/installer.nsi

!include "MUI2.nsh"
!include "LogicLib.nsh"

!ifndef VERSION
  !define VERSION "1.0.0"
!endif
!ifndef ARCH
  !define ARCH "amd64"
!endif

Name "StorageCheck"
OutFile "../dist/StorageCheck-Setup-${VERSION}-win-${ARCH}.exe"
Unicode True
InstallDir "$LOCALAPPDATA\Programs\StorageCheck"
InstallDirRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "InstallLocation"

; 程序使用 HKCU Run 键与当前用户 DPAPI，按用户安装、无需管理员
RequestExecutionLevel user

!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_ABORTWARNING

!define MUI_WELCOMEPAGE_TITLE "欢迎安装 StorageCheck"
!define MUI_WELCOMEPAGE_SUBTITLE "磁盘空间监控与邮件告警。剩余空间低于阈值时自动发送告警邮件。"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES

!define MUI_FINISHPAGE_RUN "$INSTDIR\StorageCheck.exe"
!define MUI_FINISHPAGE_RUN_TEXT "立即启动 StorageCheck"
!define MUI_FINISHPAGE_SHOWREADME ""
!define MUI_FINISHPAGE_SHOWREADME_NOTCHECKED
!define MUI_FINISHPAGE_SHOWREADME_TEXT "开机自动启动 StorageCheck"
!define MUI_FINISHPAGE_SHOWREADME_FUNCTION SetAutostart
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

VIProductVersion "${VERSION}.0"
VIAddVersionKey ProductName "StorageCheck"
VIAddVersionKey FileDescription "StorageCheck 磁盘空间监控告警"
VIAddVersionKey ProductVersion "${VERSION}"
VIAddVersionKey FileVersion "${VERSION}.0"
VIAddVersionKey CompanyName "StorageCheck"
VIAddVersionKey LegalCopyright "(c) 2026 StorageCheck"

Section "主程序" SecMain
  SectionIn RO
  SetOutPath "$INSTDIR"
  File "/oname=StorageCheck.exe" "../dist/StorageCheck-${VERSION}-win-${ARCH}.exe"

  WriteUninstaller "$INSTDIR\uninstall.exe"

  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "DisplayName" "StorageCheck"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "Publisher" "StorageCheck"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "DisplayIcon" "$INSTDIR\StorageCheck.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck" "NoRepair" 1
SectionEnd

Section "创建桌面快捷方式" SecDesktop
  CreateShortCut "$DESKTOP\StorageCheck.lnk" "$INSTDIR\StorageCheck.exe" "" "$INSTDIR\StorageCheck.exe" 0
SectionEnd

Section -StartMenu
  CreateDirectory "$SMPROGRAMS\StorageCheck"
  CreateShortCut "$SMPROGRAMS\StorageCheck\StorageCheck.lnk" "$INSTDIR\StorageCheck.exe" "" "$INSTDIR\StorageCheck.exe" 0
  CreateShortCut "$SMPROGRAMS\StorageCheck\卸载 StorageCheck.lnk" "$INSTDIR\uninstall.exe"
SectionEnd

Function SetAutostart
  ; 与程序内「开机自启」开关一致：写 HKCU Run 键
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "StorageCheck" '"$INSTDIR\StorageCheck.exe" --minimized'
FunctionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /IM StorageCheck.exe /F'
  Sleep 500
  Delete "$DESKTOP\StorageCheck.lnk"
  Delete "$SMPROGRAMS\StorageCheck\StorageCheck.lnk"
  Delete "$SMPROGRAMS\StorageCheck\卸载 StorageCheck.lnk"
  RMDir "$SMPROGRAMS\StorageCheck"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "StorageCheck"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\StorageCheck"
  RMDir /r "$INSTDIR"
SectionEnd
