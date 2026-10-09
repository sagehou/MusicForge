# MusicForge

[English](README.md) · **简体中文**

MusicForge 为音乐收藏构建适合流媒体播放的版本。Lidarr 管理源库，MusicForge 调用 ffmpeg 生成 Opus 或 MP3，Navidrome 读取输出库提供播放。增量构建只处理需要更新的歌曲。

```text
Lidarr 音频源库（只读）
            ↓
MusicForge · SQLite · ffmpeg
            ↓
Opus 或 MP3 输出库（读写）
            ↓
Navidrome（只读）
```

一个容器、一个管理员、一组源库与输出库。镜像发布于 `ghcr.io/sagehou/musicforge`，支持 `linux/amd64` 和 `linux/arm64`。

## 支持的源音频

支持 FLAC、MP3、M4A/M4B/MP4 中的 AAC/ALAC、裸 AAC、PCM WAV/AIFF、Ogg/Vorbis/Opus、WMA、APE、WavPack 与 MKA，可混合存放。后缀不区分大小写；ffprobe 检查实际音频内容，允许内嵌封面，拒绝普通视频流。所有源文件均按所选 Opus/MP3 配置重新编码；有损源再次编码可能降低音质。

输出仍沿用源库的相对目录和文件名。`01.flac` 与 `01.mp3` 会映射到同一输出；已有登记文件受保护，冲突任务在音乐库及任务页显示错误和来源路径。重命名冲突源文件后重新扫描即可。不支持的后缀会跳过。Compose 的源路径变量保留 `FLAC_DIR` 名称以兼容现有部署，也可指向混合音频源库。

## 主要功能

- 按歌手 → 专辑 → 歌曲浏览音乐库，整次扫描／构建归为一个任务，并显示实时曲目进度。
- 支持任务暂停、继续、停止和完成记录清理，保留已生成的音频。
- 扫描支持的音频文件，在 SQLite 中索引文件和标签，转换新增或变化的歌曲。
- 内容完全相同的重命名或搬迁直接移动产物，无需重新编码。
- 保留标签与 ReplayGain，每个专辑只保存一份 `cover.jpg`。
- 替换前验证新文件，转换失败时保留已有可播放产物。
- 持久化后台任务，支持自动重试和重启恢复。
- 接收 Lidarr 导入、升级 webhook，在构建或删除批次完成后刷新 Navidrome。
- 提供中英文界面，支持本地管理员登录和可选的原生 OIDC。

MVP 不包含云同步、分布式 worker、多组音乐库、多用户或插件系统。完整行为约定见[设计基线](docs/mvp-design.md)。

## 使用 Docker Compose 部署

需要 Docker Engine、Compose v2、现有音频源库，以及空的专用输出目录。示例仅在宿主机回环地址开放端口，供反向代理转发。

1. 下载部署文件：

   ```sh
   mkdir musicforge && cd musicforge
   curl -fsSLO https://raw.githubusercontent.com/sagehou/MusicForge/main/docker-compose.yml
   curl -fsSL https://raw.githubusercontent.com/sagehou/MusicForge/main/.env.example -o .env
   ```

2. 编辑 `.env`，设置宿主机目录、`PUID`/`PGID` 和公开访问地址。可将 `MUSICFORGE_VERSION` 固定为已发布版本，例如 `v0.2.6`；`latest` 跟随稳定版本。默认路径：

   | 宿主机路径 | 容器内路径 | 访问方式 |
   | --- | --- | --- |
   | `/srv/musicforge/config` | `/config` | 读写，位于宿主机本地磁盘 |
   | `/srv/music/flac` | `/music/source` | 只读 |
   | `/srv/music/streaming` | `/music/output` | 读写，首次启用时为空 |

3. 创建可写目录并设置配置的 UID/GID。默认 `10001:10001` 的示例：

   ```sh
   mkdir -p /srv/musicforge/config /srv/music/streaming
   chown -R 10001:10001 /srv/musicforge/config /srv/music/streaming
   chmod 700 /srv/musicforge/config
   docker compose pull
   docker compose up -d
   docker compose logs musicforge
   ```

   修改所有者需要相应宿主机权限。该用户还需要读取源音频的权限。`/config` 使用适合 SQLite WAL 的本地文件系统，不放在 NFS/SMB 上。源库与输出库分别挂载。

4. 配置反向代理，将公开 HTTPS 地址转发到 `127.0.0.1:8787`，并保留 Host。`MUSICFORGE_PUBLIC_URL` 填写主地址，例如 `https://musicforge.example.com`，不带路径。其他访问地址通过下文的 `MUSICFORGE_ALLOWED_ORIGINS` 配置。应用部署在 `/`，不支持子路径。

5. 打开网页，填写容器首次启动日志中的 `setup_code`，创建唯一管理员。密码长度为 12–72 字节。创建后初始化向导永久关闭。

6. 在**设置**中使用容器内路径 `/music/source` 和 `/music/output`，启用扫描并保存。初始编码为 **Opus VBR 192kbps**。MP3 VBR 推荐 **V2**；两种编码也支持 CBR，推荐 192kbps。

MusicForge 启动时将 `/config`（含宿主机预建目录）权限设置为 `0700`，保护数据库日志和保存的密钥，因此该目录必须归配置的 UID 所有。Compose 会拒绝不存在的宿主机路径，并轮转容器日志。反向代理网络、试部署验收和回滚步骤见[生产试部署指南](docs/deployment.zh-CN.md)。

Navidrome 只读挂载同一个宿主机输出目录。容器内路径可以不同，但对应音乐库的根目录必须是此输出目录。

## 网页与语言

界面根据浏览器语言偏好选择英文或简体中文，不支持的语言回退到英文。初始化、登录页面和应用顶栏均提供语言选择。手动选择保存在当前浏览器中，切换立即生效，并保留未提交的表单输入与音乐库筛选。浏览器不允许本地存储时，选择仅在本次访问中有效。

导航、表单、状态、确认弹窗、通知、常见 API 错误、日期和数字跟随所选语言。歌曲标签、路径及原始任务和系统诊断保留原文。

| 页面 | 主要操作 |
| --- | --- |
| 概览 | 文件数量、构建比例、最近任务和存储状态 |
| 音乐库 | 搜索筛选、扫描、完整校验、重建和过期产物删除 |
| 任务 | 队列状态、日志、单项或批量重试失败任务 |
| 设置 | 路径、编码、扫描间隔、并发、集成、OIDC 和密码 |

## 远程挂载的源库

已有的 rclone／FUSE 挂载可以作为只读音频源库。MusicForge 不管理 rclone，也不负责云同步；`/config` 和输出目录仍应使用可靠的本地存储。先挂载 rclone，再启动容器，并确认配置的 UID/GID 可以读取；挂载用户不同时可能需要 `--allow-other`。

扫描先索引标签、文件大小和修改时间，不再单独进行整文件哈希预读。worker 随后将需要处理的音频完整读取一次到有容量限制的本地暂存，同时计算完整 SHA-256；编码和内嵌封面提取复用这份可跳转的副本，不依赖 rclone VFS 缓存。`--vfs-cache-mode full` 仍可按需使用。不同音频格式读取标签时可能需要读取文件头、尾部或跳转；普通扫描跳过未变化歌曲的内容读取。任务页与日志展示当前歌曲、读取字节、编码进度和暂存空间等待。

默认连续 120 秒没有进展时终止源库读取，可通过 `MUSICFORGE_SOURCE_TIMEOUT_SECONDS` 调整。哈希和枚举有进展会重置计时；ffprobe、封面提取使用单次操作时限，编码须持续推进。个别歌曲读不出来时保留旧音频与已知哈希，继续为健康歌曲排队，且本轮不判定源文件过期。每首歌曲的校验／转换自动重试两次，之后等待手动重试；健康歌曲不会因为另一首失败而重新下载；整个根目录离线的等待不消耗单曲重试额度。

重新挂载后要核对容器中实际可见的目录。Docker 默认私有 bind 不会自动跟随所有宿主机重新挂载，必要时重建容器。出现根目录变化提示时，先确认真实挂载可用，再保存设置确认。验收及诊断见[部署指南](docs/deployment.zh-CN.md)。

## 增量构建与文件生命周期

源库是唯一事实来源。生成的音频和封面由 MusicForge 管理，输出维护通过网页完成。

| 触发条件 | 处理方式 |
| --- | --- |
| 新增音频、内容或标签变化、输出缺失 | 自动创建构建任务 |
| 内容完全相同的重命名或移动 | 搬迁已登记产物，不重新编码 |
| 编码配置变化 | 标记**待重建**，在音乐库手动启动 |
| 普通源文件删除 | 保留输出并标记**过期 · 源已删除**，等待手动删除 |
| Lidarr 升级 | 全部替换歌曲验证成功后，删除事件明确列出的旧产物 |
| 切换输出编码格式 | 逐首验证新格式文件，再删除对应旧格式文件 |

普通扫描比较文件大小和修改时间；首次发现或变化的文件先读取标签，在媒体库中显示“等待校验和转换”，worker 再计算完整 SHA-256。大小／修改时间相同不会被当作内容哈希。

暂存目录为 `/config/source-staging`，所有 worker 共享默认 4 GiB 上限，可通过 `MUSICFORGE_STAGING_MAX_BYTES` 调整（单位为字节）。本地磁盘应容纳最大的一首源文件，并为配置写入保留至少 64 MiB。超过上限的源文件在下载前报错，增大上限后手动重试。成功、失败或暂停都会清理暂存文件，启动时清理遗留文件。失败重试、重启或源文件变化可能需要重新读取；手动完整校验及 Lidarr 升级删除旧产物前的独立内容校验仍会读取源文件。

**完整校验**重新计算全部源文件的哈希，可发现大小与修改时间均未改变的内容变化。标签变化同样重新编码，因为输出文件也包含这些标签。

源文件至少保持 30 秒不变才开始转换。编码期间变化时，丢弃临时产物并重新扫描，不计失败次数。新产物通过编码、时长、流结构与完整解码检查后才会发布。失败时保留已有可播放文件。

过期产物继续留在输出目录和 Navidrome 中。使用**批量删除过期**或**删除所选过期产物**创建删除任务，完成后刷新播放库。未登记文件不会自动覆盖或删除，路径冲突会明确报错。

保留标签和 ReplayGain。专辑共用一份外置 `cover.jpg`，优先采用源目录外置封面，再按碟号和曲目顺序选取内嵌封面。外置封面变化可独立更新，无需重新编码音频。

## 可靠性与维护

失败任务最多尝试三次：首次执行加两次自动重试，等待时间逐次延长。耗尽后等待手动重试；普通扫描不会重置未变化的失败目标。等待和中断任务在重启后继续，中断的转换从该歌曲开头重新执行。

存储离线时暂停受影响任务，扫描不完整时不将未见文件标记为过期。挂载身份和输出目录的 `.musicforge` 归属标记用于识别挂载异常，避免将缺失挂载误判为空库。主动更换源挂载后，核对路径并保存设置以确认。保留归属标记。

索引后不能修改容器内根路径。搬迁存储时调整宿主机挂载位置，保持容器路径不变。停止 MusicForge 后备份整个 `/config`，包括数据库；另行备份源库。升级时在 `.env` 选择镜像版本，再运行 `docker compose pull` 和 `docker compose up -d`。

## Lidarr 集成

在 Lidarr 中创建原生 **Webhook** 连接：

| 字段 | 值 |
| --- | --- |
| URL | `https://musicforge.example.com/api/webhook/lidarr` |
| Username | `musicforge` |
| Password | 在 MusicForge 设置中保存的独立 webhook 密钥 |
| 事件 | 导入和升级通知 |

连接测试不创建真实转换任务。原生 `Download` 事件使用 `trackFiles[].path`、`isUpgrade` 和 `deletedFiles[].path`。Lidarr 看到不同的源根目录时，填写容器内的绝对路径作为 **Lidarr 源路径前缀**。映射后的路径仍限制在源根目录内。自定义客户端也可通过同一密钥使用 Bearer 认证。

## Navidrome 集成

在设置中保存地址、用户名、密码和音乐库 ID。转换或删除批次结束后合并变化的专辑目录，再自动刷新。Navidrome 0.59.0+ 使用定向 `startScan`，旧版本执行普通扫描。目录被删除时扫描最近仍存在的父目录。**手动刷新 Navidrome** 使用已保存的配置。

集成使用 Subsonic 加盐令牌认证，不在 URL 查询参数中发送明文密码。跨不可信网络连接时使用 HTTPS。

## 认证与恢复

本地管理员拥有全部操作权限，可额外绑定一个原生 OIDC 身份。不使用认证代理请求头。

1. 将 `MUSICFORGE_PUBLIC_URL` 设置为主 HTTPS 源地址。
2. 在设置中保存签发者地址、客户端 ID 和密钥。签发者地址必须与提供方完全一致，包括末尾斜杠。
3. 在提供方注册回调地址 `https://musicforge.example.com/api/auth/oidc/callback`。
4. 在主地址使用本地管理员账号登录后点击**绑定当前 OIDC 身份**，完成提供方认证。

只有绑定的 `issuer + sub` 可使用 OIDC。应用校验 state、nonce、PKCE 和服务端会话。网页修改操作需要 CSRF 令牌，API 读取设置时不返回密钥。保留本地密码用于恢复。

本地登录和初始化按客户端限制：15 分钟内最多允许 10 次凭据失败，成功后清零。OIDC 发起使用独立额度，成功登录后清零，不占用本地恢复或已认证绑定的额度。反向代理部署需配置 `MUSICFORGE_TRUSTED_PROXIES` 及 `X-Forwarded-For`，详见[部署指南](docs/deployment.zh-CN.md)；不受信任的连接不能通过头部指定客户端 IP。

Secret 输入留空保留已有值。要删除 Navidrome 密码或 OIDC 客户端密钥，先清空对应 URL，再勾选明确的删除选项并保存。清除 OIDC 密钥须本地登录，同时撤销 OIDC 会话、流程和身份绑定。

忘记密码时，先停止应用，再通过标准输入向相同镜像与 `/config` 卷传入新密码。在 Bash 中执行：

```bash
docker compose stop musicforge
read -rs -p 'New password: ' new_password
printf '%s\n' "$new_password" | docker compose run --rm -T musicforge -reset-password-stdin
unset new_password
docker compose up -d
```

密码长度为 12–72 字节。恢复要求独占 `/config`，并撤销全部会话。

## 启动配置

可选的 `/config/config.json` 参考 [config.example.json](config.example.json)。环境变量覆盖启动配置：

| 环境变量 | 默认值 / 用途 |
| --- | --- |
| `MUSICFORGE_CONFIG_DIR` | `/config` |
| `MUSICFORGE_LISTEN` | `:8787` |
| `MUSICFORGE_PUBLIC_URL` | 空；OIDC 主地址，也自动允许浏览器访问 |
| `MUSICFORGE_ALLOWED_ORIGINS` | 空；逗号分隔的额外浏览器访问源地址 |
| `MUSICFORGE_TRUSTED_PROXIES` | 空；逗号分隔的可信反向代理 IP CIDR，用于按客户端 IP 限流 |
| `MUSICFORGE_LOG_LEVEL` | `INFO` |
| `MUSICFORGE_FFMPEG` | `ffmpeg` |
| `MUSICFORGE_FFPROBE` | `ffprobe` |
| `MUSICFORGE_SOURCE_TIMEOUT_SECONDS` | `120`；源库读取无进展超时，范围 1–3600 秒 |
| `MUSICFORGE_STAGING_MAX_BYTES` | `4294967296`；所有 worker 共享的本地源文件暂存上限，单位为字节 |

库、编码与集成设置以 SQLite 为唯一来源。日志为 JSON。公开的 `/healthz` 检查数据库存活状态，登录后的概览单独展示音乐存储可用性。启动时在事务中迁移数据库，拒绝未知的较新结构。

多域名访问时，在 `.env` 中设置：

```dotenv
MUSICFORGE_PUBLIC_URL=https://musicforge.example.com
MUSICFORGE_ALLOWED_ORIGINS=https://musicforge.home.example.com,https://musicforge-alt.example.com
```

主地址始终允许访问。所有域名的 DNS 与反向代理都指向同一个实例，代理保留 Host，并为每个 HTTPS 地址配置 HTTPS。每项包含协议和可选端口，不接受路径和通配符。不同主机可分别使用 HTTP 或 HTTPS，但同一个主机名只能使用一种协议，因为 Cookie 不按端口隔离。Cookie 按实际访问主机配置的协议设置，转发头中的域名或协议不能扩大允许列表。浏览器修改请求的来源必须同时匹配允许地址与请求 Host；登录后的修改操作仍需 CSRF 令牌。

各域名均支持本地账号登录，登录 Cookie 只属于各自域名。从其他域名发起 OIDC 会先跳转到主地址，再创建认证流程；登录完成后留在主地址。OIDC 提供方只需注册主地址的 `/api/auth/oidc/callback`。绑定身份必须在主地址使用本地账号登录，其他域名的 Settings 会提供入口。这些地址属于启动配置，在 Settings 中只读展示；修改 `.env` 后执行 `docker compose up -d` 重建容器。

使用 `config.json` 时，额外地址写入 `allowed_origins` 数组。空的 `MUSICFORGE_ALLOWED_ORIGINS` 环境变量会清空文件中的数组；需要采用文件值时，应移除 Compose 的对应环境变量条目。本功能不改变 schema 2 或认证持久化格式。旧 `0.2.x` 镜像会忽略新增启动配置，回退后仅主地址可正常使用。

## 开发与发布

遵守 [AGENTS.md](AGENTS.md)：本地仅编辑源码、文档、工作流和进行静态核查。依赖安装、编译、可执行测试和镜像构建全部通过 GitHub Actions 完成。本地不创建依赖目录、编译缓存或构建产物，锁文件通过 Actions 生成或更新。

[CI](.github/workflows/ci.yml) 构建前后端，使用真实 ffmpeg 运行带竞态检测的 Go 测试，在 Chromium 中测试实际应用，并验证两个架构的原生容器。需要时从 `canonical-source` 获取 CI 生成的锁文件和格式化的 Go 源码。

PR 和 `main` 提交仅验证，不发布。版本标签（如 `v0.1.0`）验证成功后发布对应 GHCR 镜像。稳定版本更新 `latest`，预发布版本不更新。

翻译位于 [web/src/locales](web/src/locales)，各语言保持一致的键和插值占位符。浏览器测试覆盖语言识别、切换记忆和双语流程。许可信息见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 依赖升级与持续维护

[稳定依赖更新工作流](.github/workflows/dependencies.yml)每周执行，也可以手动启动。它读取官方稳定版本，更新 npm、Go 及兼容的间接依赖、工具链、固定提交的 Actions 和媒体源码校验值，在 Actions 内生成锁文件，并创建供审查的 PR。更新分支会显式启动 CI；不会自动合并或发布镜像。重大版本升级需要检查兼容性。如果仓库设置禁止机器人创建 PR，工作流仍推送更新分支并启动 CI，提供比较链接和 `dependency-source` 文件。当前仓库禁止机器人创建 PR，可通过比较链接手动创建 PR，或由仓库管理员开启该选项；首次生成锁文件可使用 `artifact_only` 选项。

当前工具链为 Go 1.27.1、Node 26.10.0、React 19.3、Vite 8.3、TypeScript 7 和 Tailwind 4.3。运行镜像使用 Debian 13 稳定版，以及固定源码校验值的 FFmpeg 9.0.2、Opus 1.6.1 和 LAME 4.0。镜像内 `/usr/share/doc/musicforge/media` 保留对应源码压缩包和构建说明。所有安装、编译及可执行验证仍仅在 GitHub Actions 中进行。

CI 使用真实 Navidrome 容器和只读输出挂载，验证转换后的歌曲能被发现、普通源文件删除后仍可播放、手动删除后从播放库移除。刷新请求被接受后，还要等待 Navidrome 报告扫描完成，才清除本地变更记录；重启后继续处理未完成刷新。

0.2 版本使用数据库 schema 2。升级前请停止 MusicForge 并备份整个 `/config`；回退到 0.1 需要恢复对应的升级前备份。验收范围与证据参见[中文验收报告](docs/acceptance.zh-CN.md)。
