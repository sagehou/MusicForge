# 生产试部署指南

[English](deployment.md) · **简体中文**

本指南对应 v0.2.2。安装、构建和自动验证均在 GitHub Actions 执行；生产主机只拉取已发布镜像。完整功能范围见[验收报告](acceptance.zh-CN.md)。

## 部署前

- 将 `.env` 的 `MUSICFORGE_VERSION` 固定为 `v0.2.2`，避免试部署期间随 `latest` 变化。
- `/config` 必须位于宿主机本地磁盘，归 `PUID` 所有，权限 `0700`。它包含账号、OIDC/Navidrome 密钥、库索引和任务，不能公开共享。应用启动时会收紧目录权限。
- `FLAC_DIR` 必须是已存在的源库，MusicForge 只读访问；`OUTPUT_DIR` 必须是已存在、可写、首次为空的专用目录。三个目录互不包含。Compose 不会自动创建缺失路径。
- 使用同一 UID/GID 验证源库可读、输出可写。Navidrome 只读访问同一个输出库。为输出与临时文件留出足够磁盘空间。
- `MUSICFORGE_PUBLIC_URL` 填浏览器实际访问的 HTTPS 源地址，不带子路径；代理转发该站点全部路径，并保留 Host。

创建目录与启动命令见 [README](../README.zh-CN.md)。检查配置后执行 `docker compose config --quiet`、`docker compose pull`、`docker compose up -d`。容器日志默认最多保留 3 个 10 MB 文件；首次启动日志中的初始化码只用于创建管理员，不要公开分享日志。

## 反向代理网络

宿主机进程中的 Nginx/Caddy 使用 `http://127.0.0.1:8787` 作为上游。Compose 默认只绑定宿主机回环地址。

容器中的反向代理应与 MusicForge 加入同一个 Docker 网络，上游使用 `http://musicforge:8787`；代理容器里的 `127.0.0.1` 指向代理自身。若已有外部网络 `proxy`，新建 `compose.proxy.yml`：

```yaml
services:
  musicforge:
    networks:
      - proxy
networks:
  proxy:
    external: true
```

确认代理也连接到该网络，然后使用 `docker compose -f docker-compose.yml -f compose.proxy.yml up -d`。网络名称按现有部署调整。保留应用自身的本地登录/OIDC，代理只负责 HTTPS 和转发。配置 Navidrome URL 时同样使用容器可达的地址；`localhost` 指 MusicForge 容器自身。

将 `MUSICFORGE_TRUSTED_PROXIES` 配置为 MusicForge 实际看到的代理 IP CIDR，例如 `172.20.0.2/32`；多个 IPv4/IPv6 CIDR 用逗号分隔。宿主机进程代理的地址可能表现为 Docker 网桥网关，而非 `127.0.0.1`。固定代理地址，或将信任范围限制在代理专用网络。代理必须用真实客户端 IP 覆盖 `X-Forwarded-For`，或在链尾追加真实连接 IP（Nginx：`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`）。不要原样转发客户端传来的值，也不要信任 `0.0.0.0/0` / `::/0`。MusicForge 从可信连接地址开始，从右向左取第一个不受信任的地址。未配置时忽略头部，同一代理后的客户端仍共享失败额度。此配置只用于按 IP 限流，不提供 Forward Auth 或头部身份认证。

本地登录和初始化仅计凭据失败，成功清零，每个 15 分钟固定窗口允许 10 次失败；二者及 OIDC 发起的额度独立。OIDC 回调成功后清零发起额度；已发起的回调和已认证的绑定不因公开发起接口耗尽而被阻止。通过代理验收时，验证重复成功登录不会被限流，且一个测试客户端的失败不会阻止另一个客户端登录。

## 首次验收

若要先验证少量专辑，请建立独立的临时 `/config` 和输出目录，挂载一份测试源库。索引后不能在网页改根路径；完整库部署使用其自己的配置及专用输出，不复用临时实例的所有权标记。

1. `docker compose ps` 显示 healthy；通过公开 HTTPS 地址完成初始化，再退出并登录。健康接口只检查数据库，另在概览确认源/输出存储在线。
2. 保持并发为 1、默认 Opus VBR 192 kbps；确认一张专辑转换成功，标签和单份 `cover.jpg` 正确。再扫描一次，应没有新增转换任务。
3. 在 Navidrome 中确认歌曲实际可播放。保存集成配置后手动刷新，确认任务成功及曲目出现。
4. 只在测试源库中删除一首 FLAC：扫描后应显示过期，输出和 Navidrome 条目仍保留。通过 MusicForge 手动删除过期产物并刷新后，条目才移除。
5. 修改编码参数，应显示待重建；手动启动后新文件验证成功才替换旧文件。重启容器，确认已完成曲目不重复转换、待处理任务继续。
6. 如使用 OIDC，先保留本地恢复密码，再绑定并从另一个浏览器会话测试登录。重新绑定身份会撤销此前的 OIDC 会话；本地会话保留。测试 Lidarr 的连接测试、一次导入和一次升级。

自动测试使用真实 ffmpeg、Navidrome、Chromium 和双架构镜像；Lidarr 使用原生载荷，OIDC 使用签名测试提供方。因此你实际使用的 Lidarr、Authentik/其他 OIDC 提供方、反向代理和挂载组合仍需要上述现场验收。当前音乐库 API 全量返回索引，网页每五秒轮询；尚未对大型曲库做容量压测，首次导入请观察内存和页面响应。源目录中的符号链接会使扫描暂停，需要使用真实目录或挂载路径。

## 升级与回滚

停止 MusicForge 后，备份整个 `/config`。需要完整回滚库状态时，同时保存对应时点的输出目录快照和原镜像版本；备份期间保持 MusicForge 停止。保留独立 FLAC 备份。

schema 2 是当前稳定 MVP 基线，`0.2.x` 保持数据库结构以及 Settings、任务、恢复记录的持久化格式兼容。v0.2.0/v0.2.1 → v0.2.2 没有 schema 迁移。同系列的软件回退可沿用现有 `/config`；先暂停后台任务并停止应用，再将镜像版本固定到对应旧版本，启动后检查账号、库和任务，再决定恢复后台工作。无需通过改数据库版本号来回退。

CI 的 `Same-schema rollback with published releases` 在 AMD64、ARM64 上使用固定摘要的 v0.2.1、v0.2.0 镜像，读取候选版已经生成的真实歌曲、封面、凭据和待处理任务，保存设置后再切回候选版，检查数据库与产物状态。每次成功运行生成 `rollback-amd64` / `rollback-arm64` 证据文件。同 schema 编号只是一个条件，旧版本读写的行为也必须兼容。

软件回退会使用当前库状态；若要回到升级前的完整状态，必须恢复同一时点的 `/config` 备份和输出快照。切回旧镜像不会撤销已经完成的歌曲替换、搬迁和删除。

v0.1 无法读取 schema 2，回退到 v0.1 必须恢复匹配的升级前备份。将来需要不兼容持久化变更时，必须通过明确标注的破坏性版本发布，并提供升级备份和经 CI 验证的恢复回滚路径。不要手工降低 `user_version`；这不会转换真实数据结构或恢复已删除的数据。

异常时先在设置关闭后台任务，保留日志和挂载状态。不要删除 `.musicforge`、数据库或旧播放文件来尝试修复；“源挂载已改变”需要先核对真实挂载，再保存设置确认。密码恢复命令见 README。
