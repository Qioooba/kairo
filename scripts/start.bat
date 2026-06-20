@echo off
REM OpsToolbox Windows 绿色版启动脚本
REM 双击即可：自动 cd 到本脚本所在目录，启动 OpsToolbox.exe，
REM 启动后浏览器会打开 http://127.0.0.1:18080。
REM 关闭本窗口不会关闭 OpsToolbox；如需关闭请到任务管理器结束 OpsToolbox.exe。

chcp 65001 >nul
cd /d "%~dp0"

if not exist OpsToolbox.exe (
  echo.
  echo [错误] 当前目录找不到 OpsToolbox.exe
  echo        请确认整个 OpsToolbox 目录都拷贝过来了。
  echo.
  pause
  exit /b 1
)

if not exist config.yaml (
  echo.
  echo [警告] 当前目录没有 config.yaml
  echo        如果你只是想先看一眼界面，可以直接继续。
  echo.
)

echo OpsToolbox 启动中，工作目录：%cd%
start "" OpsToolbox.exe
echo OpsToolbox 已在后台启动，浏览器应自动打开 http://127.0.0.1:18080
echo 如需关闭 OpsToolbox，请在任务管理器结束 OpsToolbox.exe。
timeout /t 5 >nul
