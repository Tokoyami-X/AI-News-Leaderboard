$ErrorActionPreference = 'Stop'
$exe = Join-Path $PSScriptRoot 'AI-Radar.exe'
$ico = Join-Path $PSScriptRoot 'AI-Radar.ico'
if (-not (Test-Path $exe)) { throw "找不到程序：$exe。请将此脚本与 AI-Radar.exe 放在同一文件夹。" }
$desktop = [Environment]::GetFolderPath('Desktop')
$shortcutPath = Join-Path $desktop 'AI Radar - AI 新闻与排行榜.lnk'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = $exe
$shortcut.WorkingDirectory = $PSScriptRoot
$shortcut.Description = '启动 AI Radar AI 新闻与排行榜'
if (Test-Path $ico) { $shortcut.IconLocation = "$ico,0" } else { $shortcut.IconLocation = "$exe,0" }
$shortcut.Save()
Write-Host "已创建桌面快捷方式：$shortcutPath" -ForegroundColor Green
Write-Host '双击桌面快捷方式即可启动服务并在 Edge 打开 AI 排行榜。'