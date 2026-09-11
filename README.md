# frpc客户端 （改自徐大大的飞牛frpc项目 感谢徐大大的支持❤️）

在 Debian 12 / 13（x86_64）上运行的 frpc Web 管理面板。

打开网页：`http://服务器IP:9999`

仓库：https://github.com/jiumian8/frpc-linux

## 功能

- 网页管理 frpc，默认端口 `9999`
- 安装 / 卸载 / 检测更新 三个菜单
- 安装时选择 GitHub 加速源
- 安装时设置网页登录账号密码
- systemd 开机自启，Web 服务异常退出后自动重启
- 检测更新会下载最新官方 `frpc` 二进制

## 一键安装

必须用 root：

```bash
curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/jiumian8/frpc-linux/main/install.sh -o install.sh && sudo bash install.sh
```

菜单：

```text
1) 安装
2) 卸载
3) 检测更新
0) 退出
```

也可以直接：

```bash
sudo bash install.sh 1
sudo bash install.sh 2
sudo bash install.sh 2 --purge
sudo bash install.sh 3
```

### 安装流程

1. 选择 GitHub 加速源
2. 设置网页登录用户名和密码
3. 自动安装依赖、下载源码、编译 Web UI、下载最新 frpc
4. 注册 systemd 服务 `frpc-web` 并开机自启

加速源：

```text
1) https://gh-proxy.org
2) https://v4.gh-proxy.org
3) https://v6.gh-proxy.org
4) https://cdn.gh-proxy.org
5) https://axisnow.gh-proxy.org
6) GitHub 直连
```

某个源失败会自动试其他源。

### 网页登录

安装时设置的账号密码用于打开管理页面。

- 用户名：3-32 位，只能是字母、数字、点、下划线、中划线
- 密码：至少 8 位
- 密码只存哈希，不明文保存
- 登录失败 5 次会锁定 5 分钟
- 登录后 12 小时内保持登录状态

装完后访问：

```text
http://服务器IP:9999
```

会先进入登录页，不是浏览器自带弹窗。

## 常用命令

```bash
sudo systemctl status frpc-web
sudo systemctl restart frpc-web
sudo journalctl -u frpc-web -n 80 --no-pager
sudo bash /root/install.sh
```

## 目录

| 路径 | 说明 |
| --- | --- |
| `/var/apps/frpc/target/ui` | Web UI |
| `/var/apps/frpc/target/app/frpc` | frpc 二进制 |
| `/var/apps/frpc/shares/frpc` | 实例配置 |
| `/var/apps/frpc/var` | 日志、登录信息和运行时文件 |
| `/etc/systemd/system/frpc-web.service` | systemd 服务 |

卸载默认保留配置。彻底删除：

```bash
sudo bash install.sh 2 --purge
```

## 更新本仓库后如何重新安装

把最新文件推到 GitHub，再在服务器执行：

```bash
curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/jiumian8/frpc-linux/main/install.sh -o install.sh && sudo bash install.sh
```

选 `1` 安装。如果已有登录账号，脚本会问是否重新设置。
