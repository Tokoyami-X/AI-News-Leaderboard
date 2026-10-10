# AI Radar — AI 最新新闻聚合

中文优先、响应式 AI 新闻网页。Go 标准库后端 + HTML/CSS/JavaScript 前端，无需 API Key 或第三方 Go 依赖。

## 功能
- 聚合公开 RSS / Atom 新闻并按发布时间排序
- 关键词搜索、来源筛选、手动刷新
- 展示来源、时间、摘要和原文链接
- 中英文切换：英文显示原始新闻，中文模式按需翻译标题与摘要；翻译结果缓存在服务内存中
- 翻译通过无需 API Key 的公开翻译服务完成；若服务不可用则回退显示原文
- 每个新闻源并行抓取、10 秒单次请求超时与备用 Feed；5 分钟缓存
- 新闻源异常时显示提示，不用虚构内容填充

## 新闻源
- OpenAI News: https://openai.com/news/
- Google AI / Labs: https://blog.google/technology/google-labs/
- Anthropic News: https://www.anthropic.com/news
- Hugging Face Blog: https://huggingface.co/blog

Feed 地址可能由上游调整。Anthropic 使用社区维护的 RSS 镜像并以 Google News 搜索 RSS 作为备用；Hugging Face 官方 Feed 超时后会回退到 Google News 搜索 RSS。各来源并行抓取，单一来源失败不会卡住整个页面。首次运行仍需网络能访问这些网站。

## Windows 启动
1. 安装 Go 1.23 或更高版本，并确认 PowerShell 中 go version 可用。
2. 双击项目根目录中的 start.bat，或在 PowerShell 进入项目根目录后运行 go run .
3. 打开 http://localhost:8080。停止时在启动窗口按 Ctrl+C。

## API
- GET /api/news
- GET /api/news?source=openai
- GET /api/news?q=agent
- GET /api/news?refresh=1
- GET /api/sources- GET /api/leaderboard?category=text（LMArena 模型排行榜；category 可选择分类）

## AI 排行榜
- 页面：`/leaderboard.html`
- 数据源：`lmarena-ai/leaderboard-dataset` 的 `latest` 数据集快照，无需 API Key。
- 后端在首次访问或距上次成功同步超过 24 小时时自动刷新；页面每分钟检查缓存状态，也可手动触发刷新。
- 上游排行榜只在 LMArena 发布新榜时更新；“每日检查”不代表上游每天必定产生新排名。网络或上游不可用时保留最近一次成功缓存。
