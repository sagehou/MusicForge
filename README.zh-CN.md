# MusicForge

[English](README.md) · **简体中文**

原始音乐留好，日常听歌轻一点。

MusicForge 把已有音乐库转换成适合串流播放的 Opus 或 MP3。有新歌或源文件变化时，只处理需要更新的部分，原始文件始终只读。常见搭配是 Lidarr → MusicForge → Navidrome；没有 Lidarr 也没关系，直接扫描音乐目录就能用。

```text
你的音乐收藏 → MusicForge → 流媒体音乐库 → Navidrome
   只读          ffmpeg       Opus / MP3        只读
```

一个容器、一个管理员、一份源库和一份输出库。镜像支持 `linux/amd64`、`linux/arm64`：`ghcr.io/sagehou/musicforge`。

## 能帮你做什么

- **少做重复转换。** 新歌、改过的歌自动排队；内容相同、只是搬了位置的文件直接搬迁输出，不再编码。标签、ReplayGain 和专辑封面也会带过去。
- **知道任务进行到哪了。** 按歌手、专辑、曲目浏览，查看读取和转换进度，暂停、继续或重试队列。替换失败时，原来能播放的文件还在。
- **补齐带时间轴的歌词。** 使用本地 LRC，也可以勾选在线来源并排序。先预览再选择，支持导入 LRC、调整时间轴、附带来源已有的译文或罗马音。
- **整理日常听的那一份。** 修正输出标签和封面，补全响度标签，检查质量问题，映射播放列表，找出疑似重复音频。最近的编辑可以对照查看和恢复。
- **让后台工作配合你的作息。** 设置处理时间段和并发数，优先处理还没有输出的新歌，用 webhook 或 ntfy 接收提醒。
- **接入已有的音乐服务。** 接收 Lidarr 导入／升级事件，完成后刷新 Navidrome，也支持 OIDC 登录。界面有中英文、明暗主题，手机上也能操作。

MusicForge 负责准备音乐库，内置试听方便核对编辑结果；日常播放交给你的播放器。下载、云端挂载管理、多用户和多音乐库不在当前范围内。

## 用 Docker Compose 开始

准备 Docker Engine、Compose v2、已有音乐目录，以及一个**专用且初始为空的输出目录**。`/config` 要放在适合 SQLite 的本地磁盘上，不要用 NFS 或 SMB。

1. 下载部署文件：

   ```sh
   mkdir musicforge && cd musicforge
   curl -fsSLO https://raw.githubusercontent.com/sagehou/MusicForge/main/docker-compose.yml
   curl -fsSL https://raw.githubusercontent.com/sagehou/MusicForge/main/.env.example -o .env
   ```

2. 编辑 `.env`，填写宿主机目录和 `PUID`／`PGID`。建议从 [Releases](https://github.com/sagehou/MusicForge/releases) 选择版本填入 `MUSICFORGE_VERSION`；想跟随稳定版也可以用 `latest`。

   | 宿主机设置 | 容器内路径 | 用途 |
   | --- | --- | --- |
   | `CONFIG_DIR=/srv/musicforge/config` | `/config` | 读写，本地磁盘 |
   | `FLAC_DIR=/srv/music/flac` | `/music/source` | 只读，可混放多种格式 |
   | `OUTPUT_DIR=/srv/music/streaming` | `/music/output` | 读写，专用输出目录 |

   `FLAC_DIR` 只是沿用的部署变量名，并不限制只能放 FLAC。三个目录要彼此独立。

   启动前也要设置 `MUSICFORGE_PUBLIC_URL`：在 Docker 宿主机本地访问时填 `http://localhost:8787`；通过反向代理访问时，填实际的 HTTPS 地址。

3. 创建可写目录。使用默认 UID／GID 时，按宿主机权限执行：

   ```sh
   mkdir -p /srv/musicforge/config /srv/music/streaming
   sudo chown 10001:10001 /srv/musicforge/config /srv/music/streaming
   sudo chmod 700 /srv/musicforge/config
   docker compose config --quiet
   docker compose pull
   docker compose up -d
   docker compose logs musicforge
   ```

   同一 UID 还需要读取源音乐的权限。`/config` 保存账号和密钥，启动时会限制其访问权限；宿主机挂载路径不存在时，Compose 会拒绝启动。

4. 示例只监听 `127.0.0.1:8787`。远程访问时，通过反向代理把实际的 HTTPS 地址转发到这里。应用部署在 `/`，不支持子路径。详见[代理设置](docs/deployment.zh-CN.md#反向代理与访问地址)。

5. 打开页面，填写首次启动日志中的 `setup_code`，创建管理员账号。在**设置**中填写容器路径 `/music/source`、`/music/output`，启用音乐库并保存，再启动扫描。默认是 **Opus VBR 192 kbps**；播放器需要 MP3 时，建议选 **MP3 VBR V2**。

给 Navidrome 只读挂载同一个输出目录。可以先拿一张小专辑试试：看标签、播一首歌，再扫一次，确认已完成的歌曲被跳过。[部署说明](docs/deployment.zh-CN.md)里有集成、远程挂载、账号恢复和升级方法。

## 平时怎么用

| 你做的事 | MusicForge 的处理 |
| --- | --- |
| 新增音乐或修改源文件 | 构建新增／变化的曲目 |
| 搬动内容完全相同的源文件 | 搬迁已登记输出，不重新编码 |
| 修改编码配置 | 标记“待重建”，由你决定何时启动 |
| 普通删除源文件 | 输出标记“过期”并继续保留，直到你明确删除 |
| Lidarr 升级替换文件 | 新文件验证通过后，清理事件中明确被替换的输出 |
| 替换失败 | 保留原有可播放文件，显示失败原因 |

普通扫描按文件大小和修改时间判断变化。如果某个工具改了内容却保留这两个值，请运行**完整校验**。未登记的输出文件不会被悄悄覆盖。`01.flac` 和 `01.mp3` 这类同名源会共用输出名，需要重命名其中一个来解决冲突。

搜索、筛选和排序交给服务端处理，每页加载 50 个条目。翻页不会丢掉已选歌曲；页面没变化时复用缓存，打开工作台才加载维护详情。

**音乐库 → 维护工作台**集中放着标签、封面、响度、播放列表和质量检查工具。常用筛选可以存成当前浏览器的视图。**任务**页展示队列、维护批次，以及按原因分组的失败。符合条件的原生 FLAC 转换失败后，可以明确选择“尽力修复”；结果会持续标记“可能受损”，请先试听。

## 歌词，日语也可以

MusicForge 只使用真实的同步歌词，不会给纯文本猜一条时间轴。

先到**设置 → 歌词设置**勾选来源、调整顺序并保存。源目录同名 `.lrc`／`.LRC` 优先；在线结果先比较匹配分，同分再看你保存的优先级。

| 来源 | 适用情况 |
| --- | --- |
| QQ 音乐、酷狗、网易云、LRCLIB、酷我、咪咕 | 常用在线候选，收录范围和可用性随录音版本、地区而异 |
| PetitLyrics | 补充日语同步歌词 |
| [AMLL TTML DB](https://github.com/amll-dev/amll-ttml-db) | 可选社区来源，包含日语；接口缺少完整录音时长，需要预览后手选 |
| [Unison](https://unison.betterlyrics.org/docs) | 可选社区来源，将支持的 TTML 原始行时间转换为 LRC |

两个社区来源和自动补齐默认关闭。自动匹配默认门槛为 96/100，同时检查录音身份和时长。分高只是更值得看一眼，不代表现场版、重制版一定选对了。

**补齐歌词**会跳过已有合适歌词的歌曲，重启或修改设置也不会反复搜索。源 LRC 更新了，或者输出歌词被手动删了，请用逐曲**重新搜索歌词**。想换个版本，用**选择歌词**预览；时间对不上，可以在编辑器里调整。搜索失败会保留原文件，在程序外手动修改过的歌词也有覆盖保护。

**设置 → 歌词设置 → 歌词源诊断**会汇总最近的搜索结果与限流等待时间。选一首歌就能单独检查来源，不会保存歌词；候选列表会说明歌名、艺人、版本、专辑或时长哪里不符。

在线服务可能不可用或限流。网易云在允许的情况下使用游客会话，也可以挂载[私有 Cookie 文件](docs/deployment.zh-CN.md#可选的网易云歌词登录)。匹配、重试和文件归属的完整规则见[歌词行为基线](docs/mvp-design.md#lrc-歌词维护)。

## 支持的源格式

FLAC、MP3、M4A／M4B／MP4 中的 AAC／ALAC、原始 AAC、PCM WAV／AIFF、Ogg／Vorbis／Opus、WMA、APE、WavPack、MKA 可以混放。后缀不区分大小写，ffprobe 会检查实际音频并拒绝普通视频流。所有源都会按选定配置重新编码，包括有损输入；再次有损编码可能降低音质。

## 远程挂载的源音乐库

已有的 rclone／FUSE 挂载可以作为只读源。MusicForge 会先把本次需要处理的源文件读入有容量上限的本地暂存，再开始编码；挂载本身由你管理。配置和输出请放在可靠的本地存储上，并为默认共享 4 GiB 的暂存空间留出余量。详见[挂载与排错](docs/deployment.zh-CN.md#远程挂载与排错)。

## 升级与求助

升级前先停止 MusicForge，备份 `/config`。要能完整恢复库状态，还需要同一时点的输出快照。然后修改 `.env` 中的镜像版本，拉取并重建容器。仅换回旧镜像，不能撤销已经完成的文件操作。[升级说明](docs/deployment.zh-CN.md#升级与回滚)介绍了 schema 2 兼容和回滚方法。

启动变量、多域名、OIDC、忘记密码的处理都在[部署说明](docs/deployment.zh-CN.md)。反馈问题时，请附上镜像版本、相关任务／路径和去掉凭据的日志，也说说你原本希望发生什么、实际看到了什么。

## 参与开发

Go 验证、真实应用浏览器验证和 rclone 验证分为独立任务；浏览器失败后重跑该任务，会保留已经通过的 Go 结果。

开发和发版规则见 [AGENTS.md](AGENTS.md)。依赖安装、构建和可执行测试**只在 GitHub Actions 运行**，本地用于修改源码和静态检查。CI 根据完整改动选择文档、界面或完整验收；版本标签发布经过检查的 AMD64／ARM64 镜像，普通分支和 PR 不发布镜像。

- [功能与兼容性基线](docs/mvp-design.md)
- [界面设计约定](DESIGN.md)
- [验证覆盖与限制](docs/acceptance.zh-CN.md)
- [第三方许可说明](THIRD_PARTY_NOTICES.md)

翻译位于 [web/src/locales](web/src/locales)，修改时请同步两种语言和占位参数。依赖版本以清单和锁文件为准；[依赖更新工作流](.github/workflows/dependencies.yml)会准备可审查的更新，不会自动合并或发版。
