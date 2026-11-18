; EnvSwitch per-user NSIS 安装器（免管理员）
; 构建：makensis /DVERSION=v1.0.0 build\nsis\envswitch.nsi
; 产物：build\bin\nsis\EnvSwitch-Setup-${VERSION}.exe

!ifndef VERSION
  !define VERSION "v0.0.0-dev"
!endif

; 以脚本自身位置为基准，避免受 makensis 工作目录影响
!define ROOT "${__FILEDIR__}\..\.."

Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "WinCore.nsh"
!include "x64.nsh"

Name "EnvSwitch ${VERSION}"
OutFile "${ROOT}\build\bin\nsis\EnvSwitch-Setup-${VERSION}.exe"
InstallDir "$LOCALAPPDATA\Programs\EnvSwitch"
RequestExecutionLevel user   ; per-user，全程不提权
SetCompressor /SOLID lzma

!define MUI_ABORTWARNING
!define MUI_ICON "${ROOT}\build\windows\icon.ico"
!define MUI_UNICON "${ROOT}\build\windows\icon.ico"

; —— 安装页 ——
!define MUI_WELCOMEPAGE_TEXT "本安装程序将安装 EnvSwitch ${VERSION}。$\r$\n$\r$\nEnvSwitch 采用 MIT License + 附加使用条款的复合授权（不是纯粹的 MIT 项目）：允许学习、研究与个人非商业使用，禁止商业用途、禁止二次分发牟利、禁止改名换皮冒充官方。继续安装即表示你已阅读并同意安装目录下 LICENSE.txt 中的条款。"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\EnvSwitch.exe"
!define MUI_FINISHPAGE_RUN_TEXT "启动 EnvSwitch 并开启开机自启"
!define MUI_FINISHPAGE_RUN_FUNCTION "LaunchAndAutostart"
!insertmacro MUI_PAGE_FINISH

; —— 卸载页 ——
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"

Function LaunchAndAutostart
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "EnvSwitch" '"$INSTDIR\EnvSwitch.exe" --tray'
  Exec '"$INSTDIR\EnvSwitch.exe" --tray'
FunctionEnd

Section "EnvSwitch"
  SetOutPath "$INSTDIR"
  File "${ROOT}\build\bin\EnvSwitch.exe"
  File "${ROOT}\build\bin\envs.exe"
  File "${ROOT}\logo.png"
  ; 许可与第三方声明随安装物一并落地，保证条款对使用者可见
  File /oname=LICENSE.txt "${ROOT}\LICENSE"
  File /oname=THIRD-PARTY-NOTICES.txt "${ROOT}\THIRD-PARTY-NOTICES.md"

  ; 快捷方式（开始菜单 + 桌面）
  CreateDirectory "$SMPROGRAMS\EnvSwitch"
  CreateShortcut "$SMPROGRAMS\EnvSwitch\EnvSwitch.lnk" "$INSTDIR\EnvSwitch.exe"
  CreateShortcut "$SMPROGRAMS\EnvSwitch\卸载 EnvSwitch.lnk" "$INSTDIR\Uninstall.exe"
  CreateShortcut "$DESKTOP\EnvSwitch.lnk" "$INSTDIR\EnvSwitch.exe"

  ; PATH 全部交给程序自身维护（归属判断单一事实源在 settings.IsOwnedPath）
  nsExec::ExecToLog '"$INSTDIR\envs.exe" path ensure'

  ; WebView2 Runtime 检测（缺失仅提示，不阻塞安装）
  ReadRegStr $1 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $1 == ""
    MessageBox MB_OK|MB_ICONINFORMATION "未检测到 WebView2 Runtime。$\r$\nEnvSwitch 界面依赖它；若窗口无法打开，请安装 Microsoft Edge WebView2 Runtime（微软官方免费组件）。"
  ${EndIf}

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\EnvSwitch" "DisplayName" "EnvSwitch"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\EnvSwitch" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\EnvSwitch" "Publisher" "沐晖"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\EnvSwitch" "DisplayIcon" "$INSTDIR\EnvSwitch.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\EnvSwitch" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\EnvSwitch" "NoModify" 1
SectionEnd

Section "Uninstall"
  nsExec::ExecToLog 'taskkill /f /im EnvSwitch.exe'
  ; 先让程序自己把 PATH 五条归属项清干净（exe 此刻还在）
  nsExec::ExecToLog '"$INSTDIR\envs.exe" path restore'
  nsExec::ExecToLog 'taskkill /f /im envs.exe'
  Delete "$INSTDIR\EnvSwitch.exe"
  Delete "$INSTDIR\envs.exe"
  Delete "$INSTDIR\logo.png"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\THIRD-PARTY-NOTICES.txt"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$DESKTOP\EnvSwitch.lnk"
  Delete "$SMPROGRAMS\EnvSwitch\EnvSwitch.lnk"
  Delete "$SMPROGRAMS\EnvSwitch\卸载 EnvSwitch.lnk"
  RMDir "$SMPROGRAMS\EnvSwitch"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "EnvSwitch"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\EnvSwitch"

  ; 数据目录（版本库/配置）默认保留，用户确认才删
  MessageBox MB_YESNO|MB_ICONQUESTION "同时删除版本库与配置？$\r$\n$LOCALAPPDATA\EnvSwitch（已安装的版本会被清除）" IDNO keepdata
    RMDir /r "$LOCALAPPDATA\EnvSwitch"
  keepdata:
SectionEnd
