# 部署与日常维护

[English](deployment.md) · **简体中文**

首次启动看 [README](../README.zh-CN.md#用-docker-compose-开始)。这份说明补充宿主机、存储和外部服务的配置。从 [Releases](https://github.com/sagehou/MusicForge/releases) 选择已发布的稳定版本，部署机器只需拉取镜像，不必自行构建。

## 存储与权限

- `/config` 使用适合 SQLite WAL 的本地磁盘，所有者与 `PUID` 一致。启动时会设为 `0700`；其中保存账号、凭据、音乐库索引、任务和恢复记录。
- 源目录只读挂载，专用输出目录读写挂载。输出首次启用时必须为空。三个根目录不能彼此包含，宿主机路径不存在时 Compose 会拒绝启动。
- 配置的 UID／GID 需要源目录读取权限，以及输出／配置目录写入权限。Navidrome 只读挂载同一个物理输出目录，容器内路径可以不同。
- 为输出和临时文件预留空间。源暂存位于 `/config/source-staging`，所有任务默认共享 4 GiB 上限；单个源文件必须放得下，并至少留 64 MiB 给配置写入。成功、失败、取消后都会清理暂存，启动时也会清理遗留文件。
- 安全创建 LRC 要求输出存储支持硬链接。源目录中的符号链接会暂停扫描，请改用实际目录或挂载路径。

建立索引后，设置中不能再更改容器根路径。搬迁存储时调整宿主机挂载，沿用原容器路径。存储工作台可以检查挂载、校验所选输出、预览迁移清单，但不会复制数据或应用新路径。

## 反向代理与访问地址

默认 Compose 监听 `127.0.0.1:8787`。宿主机上的 Nginx／Caddy 可转发到 `http://127.0.0.1:8787`。`MUSICFORGE_PUBLIC_URL` 填实际访问地址，包含协议、可选端口，不带子路径。代理转发整个站点并保留 Host。认证由 MusicForge 完成，代理身份头不会授予访问权限。

容器化代理应与 MusicForge 加入同一个 Docker 网络，转发到 `http://musicforge:8787`；代理容器里的 `127.0.0.1` 指的是代理自己。已有名为 `proxy` 的外部网络时，可增加 `compose.proxy.yml`：

```yaml
services:
  musicforge:
    networks:
      - proxy
networks:
  proxy:
    external: true
```

代理也加入此网络，然后使用两个文件启动：`docker compose -f docker-compose.yml -f compose.proxy.yml up -d`。

需要多个域名时，在 `.env` 设置：

```dotenv
MUSICFORGE_PUBLIC_URL=https://musicforge.example.com
MUSICFORGE_ALLOWED_ORIGINS=https://musicforge.home.example.com,https://musicforge-alt.example.com
```

主地址自动允许访问。为每个域名配置 DNS、代理和 HTTPS。不接受通配符和子路径；同一主机名只用一种协议。各域名使用独立的 host-only 登录 Cookie。浏览器写入必须同时匹配允许地址和请求 Host，并通过 CSRF 校验；转发的 host／protocol 头不能增加允许地址。

从其他域名开始 OIDC 登录时，会先跳到主地址，并在完成后留在那里。只登记主地址的回调，绑定身份也在主地址本地登录后进行。修改启动配置后重建容器，并逐个测试登录、保存设置和退出登录。

`MUSICFORGE_TRUSTED_PROXIES` 填写 **MusicForge 实际看到的代理 IP CIDR**，多个值用逗号分隔。宿主机代理可能显示为 Docker 网桥网关。固定代理地址或使用隔离网络，不要信任 `0.0.0.0/0`、`::/0`。代理必须用真实客户端地址覆盖 `X-Forwarded-For`，或追加实际连接地址（Nginx：`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`），不能原样信任客户端提交的值。

MusicForge 从可信连接端向前查找第一个不可信地址。未配置可信代理时，代理后的客户端共享限流额度。本地登录／初始化每十五分钟允许十次凭据失败，成功后清零；OIDC 发起使用独立额度。请确认一个客户端输错密码，不会阻止另一个客户端登录。

## 连接 Lidarr 与 Navidrome

在**设置 → 集成 → Lidarr**保存容器可访问的 URL 和 API Key，用于文件清单核对。同一 URL 下留空保留密钥，清空 URL 则清除连接。**音乐库 → 文件数量 · 与 Lidarr 核对**比较索引路径，不下载音频、不修改 Lidarr。结果是快照，“未索引”不等于文件已从磁盘丢失；检查路径映射后，可以重新扫描所选差异目录。

自动导入需要在 Lidarr 创建原生 **Webhook** 连接：

| 字段 | 值 |
| --- | --- |
| URL | `https://musicforge.example.com/api/webhook/lidarr` |
| Username | `musicforge` |
| Password | 在 MusicForge 保存的独立 webhook 密钥 |
| 事件 | 导入与升级 |

Lidarr 看到的源根路径不同时，设置 **Lidarr 源路径前缀**，填写绝对路径。映射结果必须位于 MusicForge 源目录内。连接测试不创建任务；导入任务会等通知中的曲目出现并完成转换，重启后继续等待。长期等待时会显示预期源路径。普通导入保留过期输出，只有升级事件才授权在新文件验证成功后清理明确被替换的输出。

Navidrome 保存 URL、用户名、密码和音乐库 ID，其库根目录应对应本实例的输出目录。刷新使用已保存的设置，合并变化目录，待刷新工作可跨重启保留。Navidrome 0.59.0+ 使用定向扫描，旧版使用普通扫描。可用**手动刷新 Navidrome**检查连接。URL 要能从 MusicForge 容器内部访问，`localhost` 指的是本容器；跨不可信网络请用 HTTPS。认证使用 Subsonic 加盐 token，不在查询参数里发送明文密码。

## OIDC 与密码恢复

本地管理员可以额外绑定一个 OIDC 身份：

1. 在 `MUSICFORGE_PUBLIC_URL` 设置主 HTTPS 地址。
2. 在设置中保存提供方的准确 issuer URL、客户端 ID 和密钥。
3. 在提供方登记 `https://musicforge.example.com/api/auth/oidc/callback`。
4. 在主地址以本地账号登录，选择**绑定 OIDC 身份**并完成流程。

只有绑定的 `issuer + sub` 能登录。保留本地密码以便恢复；重新绑定会撤销旧 OIDC 会话，本地登录仍可用。密钥字段留空表示保留；要删除 Navidrome 密码或 OIDC 密钥，先清空相应 URL，再使用明确的删除选项。删除 OIDC 密钥需要本地登录，同时清除绑定和 OIDC 会话。

忘记密码时，先停止应用，用相同镜像和 `/config` 卷从 stdin 输入新密码。在 Bash 中执行：

```bash
docker compose stop musicforge
read -rs -p 'New password: ' new_password
printf '%s\n' "$new_password" | docker compose run --rm -T musicforge -reset-password-stdin
unset new_password
docker compose up -d
```

部署使用了 Compose 覆盖文件时，这些命令也要带上。密码要求 12–72 字节。恢复需要独占 `/config`，并会撤销全部登录会话。

## 可选的网易云歌词登录

在设置 → 歌词设置中启用网易云，并调整它的优先级。网易云默认使用游客会话，在内存复用最多十天，到期后续期，重启后重新获取；默认无需账号。平台或地区限制仍可能阻止游客访问。

需要账号时，在网易云官方网站或 App 登录，并按官方提示完成手机号／账号验证。获取自己已登录网站请求的 `Cookie` 请求头，将其值作为一行保存到私有的 `netease.cookie` 文件，例如 `MUSIC_U=<你的登录值>; __csrf=<你的 csrf 值>`。文件必须含非空 `MUSIC_U`，不超过 16 KiB，不包含 `Cookie:` 前缀，不使用浏览器导出的 JSON 或 Netscape 格式。MusicForge 不执行密码／短信登录，也不绕过平台验证；这里的账号与 MusicForge 管理员账号相互独立。

使用默认 UID/GID 时，在宿主机准备文件（按实际 `PUID`／`PGID` 调整两个数字）：

```bash
sudo install -d -m 0700 -o 10001 -g 10001 /srv/musicforge/secrets
sudo install -m 0600 -o 10001 -g 10001 /dev/null /srv/musicforge/secrets/netease.cookie
sudoedit /srv/musicforge/secrets/netease.cookie
```

在 `.env` 添加 `NETEASE_SECRETS_DIR=/srv/musicforge/secrets`，再使用可选的 [Compose 覆盖文件](../docker-compose.netease.yml)：

```bash
docker compose -f docker-compose.yml -f docker-compose.netease.yml config --quiet
docker compose -f docker-compose.yml -f docker-compose.netease.yml up -d
```

后续 Compose 命令也使用这两个文件。覆盖配置以只读方式挂载该目录，并设置 `MUSICFORGE_NETEASE_COOKIE_FILE=/run/secrets/musicforge/netease.cookie`；其他部署方式可将这个环境变量指向容器内可读的绝对路径。Cookie 本身不要放入 `.env`、设置、源码或共享日志；凭据目录应独立于源库／输出库，备份也按凭据保护。默认游客访问不需要挂载文件。

每次网易云操作都会重新读取文件，程序不写回；私下更新内容并保留所有者／权限即可，目录挂载也支持原子替换文件。下一次请求检测到 Cookie 变化后立即解除凭据冷却，无需重启；HTTP `Retry-After` 限制仍生效。已配置的文件无法读取或格式无效时，暂停网易云并提示问题。登录失效／被拒或要求验证时，单独显示该来源错误并冷却三十分钟；游客会话被拒则冷却一分钟，后续重试重新获取。现有歌词及其他来源结果会保留。按提示完成官方验证并更新文件后，重新搜索曲目歌词。游客凭据只在内存中，登录 Cookie 不写入数据库，也不通过 API 返回。

## 启动配置

可选的 `/config/config.json` 格式见 [config.example.json](../config.example.json)。环境变量优先；音乐库、编码与集成设置通过界面保存在 SQLite 中。

| 变量 | 默认值／用途 |
| --- | --- |
| `MUSICFORGE_CONFIG_DIR` | `/config` |
| `MUSICFORGE_LISTEN` | `:8787` |
| `MUSICFORGE_PUBLIC_URL` | 浏览器／OIDC 主地址 |
| `MUSICFORGE_ALLOWED_ORIGINS` | 额外访问地址，逗号分隔 |
| `MUSICFORGE_TRUSTED_PROXIES` | 代理 IP CIDR，用于客户端 IP 限流 |
| `MUSICFORGE_LOG_LEVEL` | `INFO`；JSON 结构化日志 |
| `MUSICFORGE_FFMPEG` / `MUSICFORGE_FFPROBE` | `ffmpeg` / `ffprobe` |
| `MUSICFORGE_SOURCE_TIMEOUT_SECONDS` | `120`；源读取无进展超时，1–3600 秒 |
| `MUSICFORGE_STAGING_MAX_BYTES` | `4294967296`；共享源暂存上限，字节 |
| `MUSICFORGE_NETEASE_COOKIE_FILE` | 可选的只读凭据文件绝对路径 |

`config.json` 中用 `allowed_origins` 数组配置额外地址。空的 `MUSICFORGE_ALLOWED_ORIGINS` 会清空该数组；想用文件值，需要移除 Compose 对应的环境变量项。启动配置在设置中只读显示，修改后请重建容器。

## 远程挂载与排错

先建立 rclone／FUSE 挂载，再启动 MusicForge，确保其 UID 可读；必要时使用 `--allow-other`。配置和输出保留在本地。扫描先读标签和文件属性，随后把选中的源文件读入有限的本地暂存，同时计算 SHA-256。编码与内嵌封面提取复用这份可定位副本。元数据探测仍可能读文件头或跳到尾部；未变化的普通扫描跳过音频读取，不要求开启 VFS full 缓存。

具名 `fuse.rclone` 源会记录 remote 与有效目录的识别信息。同一源重新连接、仅设备／inode 改变时，下次音乐库操作可自动确认，重启后也适用。其他文件系统、通用 `--devname` 标签、源变化或没有识别记录的挂载仍使用严格身份检查。如果仍提示挂载变化，请先查看**容器内部**的真实源库与输出库，再点击**设置 → 音乐库 → 批准当前挂载状态**。按钮使用已保存路径，保留后台任务开关和未保存的设置，输出库必须保有原归属标记。宿主机重新挂载后，Docker 私有 bind 可能需要重建容器；不要确认一个因掉线而露出的空目录。

任务页显示源读取字节、编码进度和容量／调度等待。默认源读取停滞 120 秒超时；较慢的远程可调整 `MUSICFORGE_SOURCE_TIMEOUT_SECONDS` 并重建容器。个别文件失败会保留原输出，让健康曲目继续；不完整扫描不会将未见文件标记过期，整库掉线也不消耗曲目重试次数。普通任务首次尝试后还会自动重试两次，再等待手动处理；新尝试可能重新读取源文件。

暂停会停止当前编码并跨重启保留未完成工作，继续时从当前曲目开头处理。停止保留已完成音频，需要明确重试。删除已完成／已停止的历史只删除记录，不删除音频；之后扫描可能再次处理原先被失败记录阻止的目标。尽力修复与中断的手动维护不会自动恢复。

反馈问题时记录镜像版本、任务 ID、路径和时间，再查看 `docker compose logs --since 10m musicforge`。源读取、API 失败和慢请求包含阶段／路径信息。不要公开凭据或初始化码。示例 Compose 的日志轮换上限是三个 10 MB 文件。

`/healthz` 检查数据库存活，概览显示最近一次存储检查结果和时间。页面轮询与通知调度不探测空闲存储；新的掉线和空间变化在下次音乐库操作或手动检查时发现，重启后尚无结果时显示未检查。登录失效的受保护 API 返回 401，临时会话存储失败返回 503 和 `Retry-After: 1`。临时错误会保留页面与草稿，确认会话失效后才要求重新登录。音乐库在服务端筛选和排序，每页返回 50 首歌曲或 50 个分组。未变化时返回 304；服务端仍保留上一页快照时可只返回变化条目。服务端在内存缓存带状态的索引，数据库变化后重建；打开维护工作台仍会加载当前范围的完整数据。浏览器缓存会在退出登录时清空。这些改进不代表已验证最大曲库容量。

内核中的永久阻塞存储调用可能需要在宿主机恢复挂载。不要把删除 `.musicforge`、数据库或旧可播放文件当成修复办法。

## 在你的环境试一遍

先用单独的测试源／配置／输出目录和一张小专辑，再接入完整音乐库：

1. 完成初始化、退出再登录，同时确认容器健康和存储可用。
2. 使用并发 1 和默认编码转换，检查标签及 `cover.jpg`，在 Navidrome 播放。再扫描一次，未变化的曲目应被跳过。
3. **只在测试库中**删除一个源文件。输出应标记过期但仍能播放；明确删除过期输出并刷新 Navidrome 后，歌曲才应消失。
4. 修改编码配置并启动重建。在排队时重启，检查已完成文件保留、未完成普通任务继续。
5. 实际试一次 Lidarr 导入／升级、代理客户端 IP 和可选 OIDC 登录。自动化夹具无法代替你的凭据、挂载和外部服务配置验证。

自动化覆盖范围与限制见[验收说明](acceptance.zh-CN.md)。

## 备份与恢复

v0.3.0 起提供以下命令。先停止应用，准备一个由容器 UID／GID 可写、位于配置目录外的备份目录，例如 `./backups`：

```sh
docker compose stop musicforge
mkdir -p backups
# 按 .env 中的 PUID／PGID 设置 backups 的所有者。
docker compose run --rm --no-deps -v "$PWD/backups:/backups" musicforge -backup /backups/config.zip
docker compose run --rm --no-deps -v "$PWD/backups:/backups:ro" musicforge -check-backup /backups/config.zip
```

命令输出版本、schema、备份时间、实例 ID 和库路径。备份包括 `/config` 中的数据库、凭据和编辑历史，跳过暂存文件；不会覆盖已有 ZIP。检查会完整读取归档并检查数据库，不改动当前配置。备份按账号凭据保管。源音乐、输出音乐、`.env` 和单独挂载的密钥不在此归档内；趁应用仍停止时保存配套输出快照，完成后再启动。

恢复前，准备一个**新的空配置目录**并赋予相同 UID／GID。把下面的宿主机路径换成该目录：

```sh
CONFIG_DIR=/srv/musicforge/restored-config docker compose run --rm --no-deps \
  -v "$PWD/backups:/backups:ro" musicforge -restore-backup /backups/config.zip
```

恢复会先验证归档和数据库，拒绝非空目录；原配置仍保留，可以先用新目录演练。核对命令输出的版本和库路径，完整恢复时还原同一停机时点的输出快照，再把 `.env` 的 `CONFIG_DIR` 指向恢复目录并启动应用。只恢复配置不会撤销已完成的文件替换或删除。备份命令也能读取兼容的 v0.2.x schema-2 配置，应用必须先停止。

## 升级与回滚

先停止 MusicForge，备份整个 `/config`，记录旧镜像版本。需要完整恢复库状态时，在仍停止的情况下保存配套输出快照，并独立备份源音乐。修改 `MUSICFORGE_VERSION`，再使用平时的覆盖文件运行 `docker compose pull` 和 `docker compose up -d`。

`0.2.x` 与 `0.3.x` 使用 schema 2，保持已发布的持久化格式和含义。同系列**软件回滚**可以复用当前配置：禁用后台工作、停止应用、固定到旧镜像，再检查账号、音乐库和任务。旧版可能不理解较新的可选维护功能，返回支持该功能的版本前请保持后台工作关闭。不要手动降低 SQLite `user_version`；0.1 版需要恢复与其匹配的升级前备份。

软件回滚保留的是当前库状态。只有恢复同一停机时点的配置／输出快照，才能回到升级前；切换镜像不会撤销已完成的替换、搬迁或删除。已发布版本标签和镜像不会通过覆盖来实现回滚。

持久化兼容面发生变化时，CI 会让最近已发布的兼容 schema-2 镜像使用候选版本的同一组卷，然后再由候选版本打开，双架构都执行。手动 `historical_rollback` 还可覆盖所有固定的历史版本。`rollback-scope` 记录基线与决定，各架构产物记录往返结果。完整持久化兼容面未变时，文档／纯展示改动可以跳过回滚；版本标签仍需要当次依赖安全审计和两个最终原生镜像检查，符合条件的应用验证结果可以复用。准确门槛见 [AGENTS.md](../AGENTS.md)。
