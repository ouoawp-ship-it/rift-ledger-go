# 峡谷账房：服务器首次部署与日常更新

适用于新安装的 **Ubuntu 24.04 LTS、x86_64/amd64、root 用户**。项目安装器使用 Ubuntu 官方 Docker 软件源；其他系统不要直接套用。建议准备至少 2 核 CPU、2 GB 内存，并保证服务器能访问 GitHub、Docker 软件源、镜像仓库及 Telegram。

固定安装目录：`/opt/rift-ledger/rift-ledger-go`。下面标为「服务器」的命令在 SSH 终端执行，标为「Windows」的命令在自己的电脑 PowerShell 执行。只复制代码框中的命令，不复制 `root@yxlm:~#`、`>` 或网页链接的方括号。

系统重装后，原来的代码、SSH 密钥和数据可能已经丢失。首次部署会建立新库，不会找回旧账目；有旧备份时应先安排恢复，不要直接启用正式机器人。

## 1. 服务器：准备基础工具与 GitHub 权限

先执行：

```bash
apt-get update && apt-get install -y git curl ca-certificates openssh-client
```

检查 GitHub SSH 认证：

```bash
ssh -T git@github.com
```

第一次连接时，核对 [GitHub 公布的 SSH 指纹](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints)，一致后输入 `yes`。显示 `You've successfully authenticated` 表示认证成功，后面的 `does not provide shell access` 是正常提示；此测试的退出码为 1，也不代表认证失败。

如果提示 `Permission denied (publickey)`，执行以下命令。已有私钥时会保留，不覆盖：

```bash
install -d -m 700 /root/.ssh
if [ ! -f /root/.ssh/id_ed25519 ]; then
  ssh-keygen -t ed25519 -C rift-ledger-server -f /root/.ssh/id_ed25519 -N ''
fi
cat /root/.ssh/id_ed25519.pub
```

复制输出的一整行 **公钥**，到 GitHub 仓库 `ouoawp-ship-it/rift-ledger-go` → **Settings → Deploy keys → Add deploy key**，粘贴并保存。只需要拉取代码，不勾选 **Allow write access**。不要复制不带 `.pub` 后缀的私钥文件。

然后验证仓库读取权限：

```bash
git ls-remote git@github.com:ouoawp-ship-it/rift-ledger-go.git refs/heads/main
```

出现提交编号和 `refs/heads/main` 后再继续。SSH 登录成功不一定代表有这个仓库的权限，这一步才是拉取权限检查。不要在 GitHub 的 `Username` 或 `Password` 提示后输入 `cd` 等 Linux 命令；本文统一使用 SSH 克隆。

## 2. 服务器：一键首次部署

完成上一步授权后，**将下面整个代码框一次性复制执行**。它会安装所需 Docker 组件、下载 main 分支、生成管理员密钥、构建并启动服务。第一次构建会下载依赖并运行 Go 测试，需要等待；任何一步失败都会停止，不会继续假报成功。

```bash
bash <<'RIFT_DEPLOY'
set -euo pipefail
[[ "$EUID" -eq 0 ]] || { echo '请使用 root 用户执行'; exit 1; }
. /etc/os-release
[[ "$ID" == ubuntu ]] || { echo '此命令仅用于 Ubuntu'; exit 1; }
[[ "$(uname -m)" == x86_64 ]] || { echo '此安装器需要 x86_64/amd64'; exit 1; }
apt-get update
apt-get install -y git curl ca-certificates openssh-client
git ls-remote git@github.com:ouoawp-ship-it/rift-ledger-go.git refs/heads/main >/dev/null
project=/opt/rift-ledger/rift-ledger-go
[[ ! -e "$project" ]] || { echo '安装目录已存在，请检查目录或使用本文的更新步骤；未覆盖文件。'; exit 1; }
install -d /opt/rift-ledger
git clone --branch main --single-branch git@github.com:ouoawp-ship-it/rift-ledger-go.git "$project"
cd "$project"
bash scripts/init-env.sh
sed -i 's/^HOST_PORT=.*/HOST_PORT=127.0.0.1:8080/' .env
unset HOST_PORT
bash scripts/install.sh
docker compose ps
RIFT_DEPLOY
```

看到「部署成功」后，容器应为 `Up`；健康检查稍后变为 `healthy`。如果显示 Docker Compose 插件缺失，先按 [Docker 的 Ubuntu 安装说明](https://docs.docker.com/engine/install/ubuntu/)补齐插件，再进入项目目录运行 `bash scripts/install.sh`，不要重复克隆或删除目录。

本命令把 `.env` 中的 `HOST_PORT` 设为 `127.0.0.1:8080`；当前 Compose 会展开为 `127.0.0.1:8080:8080`，后台只监听服务器本机。无需在云服务器安全组开放 8080。SSH 端口应允许你的电脑访问。

如果首次构建因网络问题中断，修好网络后用下面命令继续，保留已经生成的密钥和配置：

```bash
cd /opt/rift-ledger/rift-ledger-go && bash scripts/install.sh
```

## 3. 获取管理员登录密钥

在服务器执行：

```bash
sed -n 's/^ADMIN_TOKEN=//p' /opt/rift-ledger/rift-ledger-go/.env
```

复制输出的一整行到自己的密码管理器。它是**后台登录密钥**，不是 Telegram Bot Token，不要发到群里或提交 GitHub。

## 4. Windows：打开后台

在自己的 Windows PowerShell 中执行，将 `服务器IP` 换成重装后的实际地址：

```powershell
ssh -N -L 18080:127.0.0.1:8080 root@服务器IP
```

如果 SSH 使用自定义端口，例如 2222，命令改为：

```powershell
ssh -p 2222 -N -L 18080:127.0.0.1:8080 root@服务器IP
```

连接后没有输出、窗口一直停在那里是正常现象。保持窗口打开，在同一台电脑浏览器访问：

```text
http://127.0.0.1:18080/
```

输入第 3 步的管理员密钥。关闭 SSH 隧道窗口后，网页无法连接；服务器和机器人仍独立运行。浏览器整页重新加载后需要重新输入密钥，后台的「刷新数据」按钮不会退出登录。

这里的 `127.0.0.1:18080` 是通过 SSH 隧道连接服务器，与开发时的本地预览 `127.0.0.1:18085` 不同。采用本文部署方式时，直接打开「服务器公网 IP:8080」无法访问，这是预期行为。若要免隧道访问，应另行配置域名与 HTTPS 反向代理。

## 5. 后台：配置机器人并验证

进入「机器人设置」，依次填写：

1. **Bot Token**：从 BotFather 获得的机器人密钥。之后修改其他字段时留空，会保留已保存的 Token。
2. **机器人用户名**：机器人自己的用户名，例如 `yxlmKJbot`。
3. **群 ID**：开奖、公示消息的目标群，通常为 `-100` 开头的负整数。
4. **管理员 TG ID**：接收上分、下分等通知的管理员数字 ID。
5. **客服用户名**：玩家联系的客服用户名，例如 `pddpdd`，不必加 `@`。
6. 打开 **启用 Telegram**，点击 **保存设置**，等待页面提示「配置已保存并生效」。

没有 Topic ID，无需填写话题。保存后服务会自动重启应用配置，页面会等待恢复，不需要再次点击保存。运行配置文件优先于 `.env`；配置完成后通过网页修改机器人参数，不要只改 `.env` 后误以为会覆盖已保存配置。

将机器人加入目标群，授予发送消息权限；管理员先私聊机器人发送 `/start`。随后点击「测试机器人连接」，再点击「发送群测试消息」，后者会实际向目标群发消息。

检查顶部接收状态、发送队列和群里实际消息。`healthy` 只表示网页和数据库正常，不代表 Telegram 收发正常。不要同时用另一套程序消费同一个机器人 Token。

最后进入「英雄数据」刷新数据，再在「赔率设置」保存倍率和下注限额。确认群通知、玩家私聊和上下分申请提醒正常后，再开始正式期次。

## 6. 以后更新：一条命令

已完成部署、容器正常运行时，在服务器执行：

```bash
cd /opt/rift-ledger/rift-ledger-go && bash scripts/update.sh
```

此脚本拉取当前分支最新代码、备份数据库、构建测试、重建容器并检查健康状态。备份失败时会停止更新，需要先恢复现有容器或排查备份失败原因。更新期间服务有短暂重启；配置和账本保存在 Docker 数据卷中。

不要在 `/root` 下直接运行 `docker compose` 或 `./scripts/update.sh`，该目录没有项目配置。不要使用 `docker compose down -v`，它会删除数据卷。

## 7. 日常检查与 Telegram 网络排查

每组命令都从进入项目目录开始，避免「no configuration file provided」。

检查服务：

```bash
cd /opt/rift-ledger/rift-ledger-go && docker compose ps
```

查看最近日志：

```bash
cd /opt/rift-ledger/rift-ledger-go && docker compose logs --tail=100 --timestamps app
```

查看运行版本：

```bash
git -C /opt/rift-ledger/rift-ledger-go log -1 --oneline
```

容器里检查 Telegram HTTPS：

```bash
cd /opt/rift-ledger/rift-ledger-go && docker compose exec -T app curl -4 -I --connect-timeout 10 --max-time 20 https://api.telegram.org
```

出现完整 HTTP 响应，例如 `HTTP/2 302`，说明本次 HTTPS 请求能到达目标；它不验证 Bot Token 或群权限。若超时，再分别检查宿主机网络与容器 DNS：

```bash
curl -4 -I --connect-timeout 10 --max-time 20 https://api.telegram.org
getent ahostsv4 api.telegram.org
cd /opt/rift-ledger/rift-ledger-go && docker compose exec -T app getent ahostsv4 api.telegram.org
```

宿主机能访问而容器不能访问时，优先检查容器 DNS、路由和 Docker 转发。不要重复重装应用，也不要直接粘贴旧服务器的 Telegram IP 作为永久配置。

网页出现响应不完整时，记录**发生时间、操作内容、HTTP 状态和请求编号**，配合服务器日志定位。上分、下分或结算结果未知时，先刷新核对记录，避免修改输入后当成新操作再次提交。

## 8. 备份与重装前准备

手动创建一致性数据库备份：

```bash
cd /opt/rift-ledger/rift-ledger-go && bash scripts/backup.sh
```

备份位于项目的 `backups/`。**数据库备份不包含管理员密钥和机器人配置。** 机器人已经完成配置后，可额外执行：

```bash
bash <<'RIFT_CONFIG_BACKUP'
set -euo pipefail
cd /opt/rift-ledger/rift-ledger-go
config_backup="backups/config-$(date -u +%Y%m%dT%H%M%SZ)"
install -d -m 700 "$config_backup"
install -m 600 .env "$config_backup/.env"
docker compose cp app:/data/runtime-settings.json "$config_backup/runtime-settings.json"
chmod 600 "$config_backup/runtime-settings.json"
printf '配置备份已保存到 %s\n' "$config_backup"
RIFT_CONFIG_BACKUP
```

如果从未通过网页保存机器人配置，`runtime-settings.json` 可能尚不存在；这时保留 `.env` 即可。把数据库备份和配置备份下载到自己的电脑或独立备份存储，**只留在原服务器上的备份不能防止重装丢失**。配置文件包含密钥，不要公开。

恢复旧账目涉及数据库替换和待发送队列核实，请参考 `docs/DEPLOY.md`；不提供会直接覆盖现有账目的自动恢复命令。

---

本文按当前仓库的 `scripts/install.sh`、`scripts/update.sh`、`scripts/backup.sh` 和 `compose.yaml` 核对编写。部署脚本仍需在目标服务器执行，本地检查不等于已替你部署。Docker 安装参考 [官方 Ubuntu 文档](https://docs.docker.com/engine/install/ubuntu/)；仓库授权参考 [GitHub Deploy keys](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys) 和 [SSH 连接测试说明](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/testing-your-ssh-connection)。
