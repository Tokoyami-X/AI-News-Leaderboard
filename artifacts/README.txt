AI Radar - Windows 版

1. 将 artifacts 文件夹中的 AI-Radar.exe、AI-Radar.ico 和 Create-Desktop-Shortcut.ps1 放在固定文件夹中。
2. 在该文件夹空白处按住 Shift 并右键，选择“在此处打开 PowerShell”，执行：
   powershell -ExecutionPolicy Bypass -File .\Create-Desktop-Shortcut.ps1
3. 双击桌面上的“AI Radar - AI 新闻与排行榜”启动。
4. 程序会启动本地服务，并自动使用 Microsoft Edge 打开 http://localhost:8080/leaderboard.html。可用 AI_NEWS_START_URL 环境变量更改打开页面。
5. 网页静态资源已嵌入 EXE，不需要另外携带 web 文件夹。新闻与排行榜数据需要联网。
6. 关闭运行程序的命令窗口或在任务管理器结束 AI-Radar.exe 可停止服务。