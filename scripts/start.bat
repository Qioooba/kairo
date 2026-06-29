@echo off
REM 豆包工具箱 Windows 绿色版启动脚本
REM 双击即可：自动 cd 到本脚本所在目录，启动 DoubaoToolbox.exe，
REM 启动后浏览器会打开 http://127.0.0.1:18080。
REM 关闭本窗口不会关闭 豆包工具箱；如需关闭请到任务管理器结束 DoubaoToolbox.exe。

chcp 65001 >nul
cd /d "%~dp0"

if not exist DoubaoToolbox.exe (
  echo.
  echo [错误] 当前目录找不到 DoubaoToolbox.exe
  echo        请确认整个 豆包工具箱 目录都拷贝过来了。
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

echo 豆包工具箱 启动中，工作目录：%cd%
start "" DoubaoToolbox.exe
echo 豆包工具箱 已在后台启动，浏览器应自动打开 http://127.0.0.1:18080
echo 如需关闭 豆包工具箱，请在任务管理器结束 DoubaoToolbox.exe。
timeout /t 5 >nul
