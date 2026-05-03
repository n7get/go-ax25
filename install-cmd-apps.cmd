@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "SCRIPT_DIR=%~dp0"
pushd "%SCRIPT_DIR%" >NUL

set "APPS="
for /D %%D in (cmd\*) do (
  if exist "%%D\main.go" (
    set "APPS=!APPS! .\%%D"
  )
)

if "%APPS%"=="" (
  echo No cmd apps with main.go found under .\cmd
  popd >NUL
  exit /b 1
)

echo Installing cmd apps:%APPS%
go install %APPS%
if errorlevel 1 (
  popd >NUL
  exit /b 1
)

echo Install complete.
popd >NUL
exit /b 0
