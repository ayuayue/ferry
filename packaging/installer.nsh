Var FerryWebView2Decision

Function FerryCheckWebView2
  StrCpy $FerryWebView2Decision "install"
  Call FerryFindWebView2
  StrCmp $0 "" ferry_install_webview
  IfFileExists "$0\msedgewebview2.exe" 0 ferry_install_webview
  IfFileExists "$0\EBWebView\x64\EmbeddedBrowserWebView.dll" webview_ok ferry_install_webview
ferry_install_webview:
  MessageBox MB_YESNO|MB_ICONQUESTION "Ferry 需要 Microsoft WebView2 Runtime，当前未检测到可用运行库。是否在安装完成后打开微软官方下载页？" IDYES webview_download IDNO webview_declined
webview_download:
  StrCpy $FerryWebView2Decision "download"
  Return
webview_declined:
  MessageBox MB_OK|MB_ICONEXCLAMATION "你选择了不安装 WebView2。安装完成后 Ferry 可能无法显示界面，需要安装运行库后再启动。"
webview_ok:
FunctionEnd

Function FerryFindWebView2
  StrCpy $0 ""
  StrCpy $1 0
ferry_hive_loop:
  IntCmp $1 2 0 ferry_hive_hklm ferry_hive_hkcu
ferry_hive_hklm:
  SetRegView 32
  ReadRegStr $2 HKLM "SOFTWARE\Microsoft\EdgeUpdate\ClientState\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "EBWebView"
  Goto ferry_hive_next
ferry_hive_hkcu:
  ReadRegStr $2 HKCU "SOFTWARE\Microsoft\EdgeUpdate\ClientState\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "EBWebView"
ferry_hive_next:
  IfFileExists "$2\msedgewebview2.exe" 0 +3
  StrCpy $0 "$2"
  SetRegView 64
  Return
  IntOp $1 $1 + 1
  Goto ferry_hive_loop
  SetRegView 64
FunctionEnd

Function FerryRunInstallerWebViewChoice
  StrCmp $FerryWebView2Decision "download" 0 ferry_webview_choice_done
  ExecShell "open" "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
ferry_webview_choice_done:
FunctionEnd

!macro customInit
  Call FerryCheckWebView2
!macroend

!macro customInstall
  IfFileExists "$INSTDIR\engine-setup.exe" +3 0
  MessageBox MB_OK|MB_ICONSTOP "安装包缺少 Ferry 引擎安装组件，无法继续。"
  Abort
  ExecWait '"$INSTDIR\engine-setup.exe" "$INSTDIR\engine\win\amd64"' $0
  IntCmp $0 10 ferry_official_conflict
  IntCmp $0 0 ferry_engine_installed
  MessageBox MB_OK|MB_ICONSTOP "Ferry 引擎安装失败（代码 $0）。请允许管理员授权，或稍后打开 Ferry 设置页重试。日志位于 %TEMP%\ferry-engine-install.log。"
  Abort
ferry_official_conflict:
  MessageBox MB_OK|MB_ICONEXCLAMATION "检测到官方 ProxyBridge 正在运行。应用文件已复制，但引擎尚未安装。请完全退出官方应用并停止其引擎服务，再从 Ferry 设置页安装引擎。"
  Goto ferry_engine_installed
ferry_engine_installed:
  Call FerryRunInstallerWebViewChoice
  StrCpy $FerryWebView2Decision "install"
!macroend

!macro customUnInstall
  ; Never remove an official or custom-location service on uninstall.
  ReadRegStr $0 HKLM "SYSTEM\CurrentControlSet\Services\ProxyBridgeSvc" "ImagePath"
  StrCpy $1 '"$INSTDIR\engine\win\amd64\ProxyBridgeSvc.exe"'
  System::Call 'kernel32::lstrcmpiW(w r0, w r1) i.r2'
  StrCmp $2 0 0 ferry_uninstall_engine_done
  IfFileExists "$INSTDIR\engine-setup.exe" 0 ferry_uninstall_engine_done
  ExecWait '"$INSTDIR\engine-setup.exe" "$INSTDIR\engine\win\amd64" --remove' $0
ferry_uninstall_engine_done:
!macroend
