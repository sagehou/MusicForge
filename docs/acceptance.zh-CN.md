# MusicForge 验收证据

[English](acceptance.md) · **简体中文**

约定行为以 [MVP 基线](mvp-design.md)为准。[六轮审查记录](production-review.md)列出问题、修复与验证链接。六轮审查均已通过运行验证。最终完整 [CI](https://github.com/sagehou/MusicForge/actions/runs/37025923050)包括后端 race/集成/vet 检查、八项 Chromium 浏览器检查以及 amd64/arm64 原生镜像测试。下表记录已验证行为及其范围；容器发布另外由版本标签触发并验收。

2026-10-08 生产试部署复查修复了 OIDC 重新绑定的会话撤销、密码更新并发边界、已有 `/config` 目录权限与构建依赖安全补丁。[复查 CI](https://github.com/sagehou/MusicForge/actions/runs/37717242429)已通过；新增双架构真实 Compose 检查。v0.2.1 的试部署按[部署指南](deployment.zh-CN.md)核对代理网络、目录所有权和回滚快照。镜像摘要与发布验证见 [v0.2.1 发布页](https://github.com/sagehou/MusicForge/releases/tag/v0.2.1)。

对 ds4.1-flash 的逐项复核修复了成功登录被限流计数、代理后的客户端 IP 隔离、已保存凭据的显式清除和 Lidarr 绝对路径前缀校验。[本轮 CI](https://github.com/sagehou/MusicForge/actions/runs/37732034349)通过了后端、八项浏览器检查和双架构原生镜像/Compose 检查。官方 Navidrome 0.64.2 测试已明确记录并验证 `1:.`，覆盖根级歌曲发现、封面变更和删除移除。[v0.2.2](https://github.com/sagehou/MusicForge/releases/tag/v0.2.2) 新增的可信代理配置见[部署指南](deployment.zh-CN.md)。符号链接延期、损坏源文件处理和可写性探测保留已有取舍。

本次多域名更新增加明确的 `MUSICFORGE_ALLOWED_ORIGINS`、按主机名隔离的 Cookie，并以 `MUSICFORGE_PUBLIC_URL` 作为 OIDC 主地址。[多域名 CI](https://github.com/sagehou/MusicForge/actions/runs/37746300869)通过后端 race/集成/vet、八项 Chromium 检查（含两个主机的本地登录和保存设置），以及双架构原生镜像/Compose 验证。OIDC 测试覆盖创建 state 前跳转主地址、固定主回调、拒绝其他域名的回调，并保留 PKCE 与重放防护。两个架构的回退证据均记录 v0.2.2、v0.2.1、v0.2.0 往返成功，使用同一 schema-2 数据库、启动配置、源库与输出。生产试部署使用 [v0.2.3](https://github.com/sagehou/MusicForge/releases/tag/v0.2.3) 和更新后的 Compose；旧版本保持数据兼容，回退后仅主地址可正常使用。


部署反馈改进将每次扫描／重建队列归为一个任务，加入暂停、继续、停止和记录清理，并在网页与结构化日志中显示逐曲进度。音乐库按歌手 → 专辑 → 歌曲浏览。[任务控制 CI](https://github.com/sagehou/MusicForge/actions/runs/37753296463) 通过后端竞态／集成／vet、真实 Chromium 流程，以及 AMD64／ARM64 原生镜像、Compose 和回滚检查。回归用例终止运行中的编码进程，保留原可播放音频及实际失败次数，验证暂停／分组跨重启保留，且删除记录不删除音乐库或输出。浏览器实际暂停、继续、停止、重试并删除三首歌曲组成的队列。两种架构均用 v0.2.3／v0.2.2／v0.2.1／v0.2.0 重新打开分组进度、暂停队列和停止项，执行设置写回并比较持久化状态。[v0.2.4 发布记录](https://github.com/sagehou/MusicForge/releases/tag/v0.2.4) 保存最终发布验证。新队列归组，升级前的历史继续独立控制。

| 约定 | GitHub Actions 中的验证证据 |
| --- | --- |
| Opus/MP3、VBR/CBR、标签、ReplayGain 与单份外部封面 | 真实 ffmpeg 生命周期测试；两个架构的镜像都执行 Opus/MP3 编码与探测 |
| 增量构建、完整校验与标签更新 | 覆盖保持大小/mtime 的内容变更、替换失败和未变文件不重复编码 |
| 重命名与复制、局部目录搬迁、损坏源文件 | 回归测试验证身份保留、避免重复编码以及损坏文件隔离；中间帧 CRC 损坏时拒绝替换并保留旧产物 |
| 普通删除保留、明确升级自动清理 | 升级安全回归；原生 Lidarr Download/Upgrade HTTP 请求，含 Basic/Bearer 认证 |
| 重试次数、中断恢复、原子发布与删除 | 重试及恢复测试；发布/删除日志幂等重放，保留外来替换文件 |
| 重复导入与刷新并发 | 持久化后续扫描、合并范围、重启保留与单调目录变更标记 |
| Navidrome 播放库生命周期 | 官方 0.64.2 容器只读访问输出；发现转换歌曲、保留普通删除产物、移除手动删除产物 |
| 单管理员、本地登录及 OIDC 边界 | CSRF、初始化、密码恢复、伪造代理头、签名测试提供方的 OIDC/PKCE/重放及解绑会话撤销 |
| 存储、所有权和数据库边界 | 重叠/符号链接/离线保护、实例锁、受管文件冲突；schema 1→2 保留产物、所有权及耗尽的重试预算，拒绝未来 schema |
| 中英文、分页和移动端 | Chromium 访问真实应用；语言检测与持久化、输入保留、过期删除确认、旧数据提示恢复、移动端退出与截图 |
| 稳定依赖与维护 | 官方版本元数据、Actions 生成锁文件、npm audit、每周更新分支 CI 与 PR/比较链接回退 |
| 发布 | amd64/arm64 原生验证通过后，由版本标签触发 GHCR 发布；主分支和 PR 验证不发布 |

## 已验证范围与部署限制

Lidarr 验证使用原生事件负载请求 MusicForge，CI 未启动完整 Lidarr 实例。OIDC 使用签名测试提供方，未部署 Authentik 实例；Navidrome 则运行真实当前版本容器。浏览器验收运行 Chromium，未单独认证其他浏览器。

普通扫描按约定使用大小和 mtime 快速判断。外部编辑器保留两者时，应使用完整校验。输出目录专供 MusicForge 管理；`/config` 使用适合 SQLite WAL 的宿主机本地磁盘。源与输出分别挂载，Navidrome 只读访问输出。

当前仓库禁止机器人创建 PR。维护工作流仍推送更新分支、启动 CI 并提供比较链接和源码文件；仓库管理员可开启机器人 PR。两种方式都需要人工审查合并。

升级前停止服务并备份 `/config`。0.1 版本无法打开 schema 2，回退时需要恢复匹配的升级前备份。版本标签再次执行验证后发布容器；实际发布结果、镜像摘要和架构清单记录在 [v0.2.0 发布页](https://github.com/sagehou/MusicForge/releases/tag/v0.2.0)。
