#!/bin/bash
# frpc客户端管理脚本
# 适用：Debian 12 / 13（x86_64），无需 Docker
# 访问：http://服务器IP:9999
#
# 仓库: https://github.com/jiumian8/frpc-linux
# 服务器执行：
#   curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/jiumian8/frpc-linux/main/install.sh -o install.sh
#   sudo bash install.sh

set -euo pipefail

export HOME="${HOME:-/root}"
export USER="${USER:-root}"
export XDG_CACHE_HOME="${XDG_CACHE_HOME:-${HOME}/.cache}"

GITHUB_REPO="${FRPC_WEB_REPO:-jiumian8/frpc-linux}"
GITHUB_BRANCH="${FRPC_WEB_BRANCH:-main}"

APP_NAME="frpc客户端"
APP_ROOT="/var/apps/frpc"
TARGET_ROOT="${APP_ROOT}/target"
UI_ROOT="${TARGET_ROOT}/ui"
APP_BIN_DIR="${TARGET_ROOT}/app"
FRPC_BIN="${APP_BIN_DIR}/frpc"
CONFIG_ROOT="${APP_ROOT}/shares/frpc"
VAR_ROOT="${APP_ROOT}/var"
SERVICE_NAME="frpc-web"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
DEFAULT_FILE="/etc/default/${SERVICE_NAME}"
PROXY_FILE="${APP_ROOT}/github-proxy.conf"
AUTH_FILE="${VAR_ROOT}/auth.json"
BOOTSTRAP_AUTH_FILE="${VAR_ROOT}/auth.bootstrap"
UI_LISTEN=":9999"
UI_PORT="9999"
FRP_REPO="fatedier/frp"
GH_PROXY=""

PROXY_OPTIONS=(
    "https://gh-proxy.org"
    "https://v4.gh-proxy.org"
    "https://v6.gh-proxy.org"
    "https://cdn.gh-proxy.org"
    "https://axisnow.gh-proxy.org"
)

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
CYAN=$'\033[0;36m'
BOLD=$'\033[1m'
NC=$'\033[0m'

log()  { printf '%s\n' "${GREEN}[+]${NC} $*"; }
warn() { printf '%s\n' "${YELLOW}[!]${NC} $*"; }
err()  { printf '%s\n' "${RED}[x]${NC} $*" >&2; }
die()  { err "$*"; exit 1; }

SCRIPT_PATH="$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || printf '%s' "$0")"
SRC_DIR="$(cd "$(dirname "${SCRIPT_PATH}")" 2>/dev/null && pwd || pwd)"
PAYLOAD_TMP=""

cleanup() {
    if [ -n "${PAYLOAD_TMP}" ] && [ -d "${PAYLOAD_TMP}" ]; then
        rm -rf "${PAYLOAD_TMP}"
    fi
}
trap cleanup EXIT

require_root() {
    if [ "$(id -u)" -ne 0 ]; then
        die "必须以 root 运行。请使用: sudo bash $0"
    fi
}

read_input() {
    local prompt="$1"
    local __var="$2"
    local value=""
    if [ -t 0 ]; then
        read -r -p "${prompt}" value || true
    elif [ -r /dev/tty ]; then
        read -r -p "${prompt}" value < /dev/tty || true
    else
        return 1
    fi
    printf -v "${__var}" '%s' "${value}"
}

pause_if_interactive() {
    local dummy=""
    if [ -t 0 ] || [ -r /dev/tty ]; then
        echo
        read_input "按回车返回菜单..." dummy || true
    fi
}

detect_os() {
    if [ ! -f /etc/os-release ]; then
        die "无法识别系统，仅支持 Debian 12/13"
    fi
    # shellcheck disable=SC1091
    . /etc/os-release
    if [ "${ID:-}" != "debian" ]; then
        die "当前系统是 ${PRETTY_NAME:-unknown}，本脚本仅支持 Debian 12/13"
    fi
    case "${VERSION_ID%%.*}" in
        12|13) ;;
        *) warn "当前 Debian 版本为 ${VERSION_ID}，脚本按 12/13 处理" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) ;;
        *) die "当前架构是 $(uname -m)，只支持 x86_64" ;;
    esac
}

save_proxy() {
    mkdir -p "${APP_ROOT}"
    printf '%s\n' "${GH_PROXY}" > "${PROXY_FILE}"
}

load_saved_proxy() {
    if [ -n "${FRPC_GH_PROXY:-}" ]; then
        GH_PROXY="${FRPC_GH_PROXY}"
        return 0
    fi
    if [ -f "${PROXY_FILE}" ]; then
        GH_PROXY="$(head -n 1 "${PROXY_FILE}" | tr -d '[:space:]')"
        return 0
    fi
    return 1
}

github_url() {
    local url="$1"
    if [ -z "${GH_PROXY}" ]; then
        printf '%s' "${url}"
    else
        printf '%s/%s' "${GH_PROXY%/}" "${url}"
    fi
}

choose_proxy() {
    local choice="" i=1 saved=""
    if [ -n "${FRPC_GH_PROXY:-}" ]; then
        GH_PROXY="${FRPC_GH_PROXY}"
        log "使用指定加速源: ${GH_PROXY:-GitHub 直连}"
        return 0
    fi

    echo
    printf '%s\n' "${CYAN}${BOLD}请选择 GitHub 加速源${NC}"
    if load_saved_proxy; then
        saved="${GH_PROXY}"
        if [ -n "${saved}" ]; then
            echo "  上次使用: ${saved}"
        else
            echo "  上次使用: GitHub 直连"
        fi
    fi
    echo
    for p in "${PROXY_OPTIONS[@]}"; do
        echo "  ${i}) ${p}"
        i=$((i + 1))
    done
    echo "  ${i}) GitHub 直连（不使用加速）"
    echo

    if ! read_input "请输入选项 [1-${i}]: " choice; then
        GH_PROXY="https://gh-proxy.org"
        warn "无法交互，默认使用 ${GH_PROXY}"
        save_proxy
        return 0
    fi

    case "${choice}" in
        1) GH_PROXY="https://gh-proxy.org" ;;
        2) GH_PROXY="https://v4.gh-proxy.org" ;;
        3) GH_PROXY="https://v6.gh-proxy.org" ;;
        4) GH_PROXY="https://cdn.gh-proxy.org" ;;
        5) GH_PROXY="https://axisnow.gh-proxy.org" ;;
        6|"") GH_PROXY="" ;;
        *)
            warn "无效选项，使用 https://gh-proxy.org"
            GH_PROXY="https://gh-proxy.org"
            ;;
    esac

    if [ -n "${GH_PROXY}" ]; then
        log "已选择加速源: ${GH_PROXY}"
    else
        log "已选择 GitHub 直连"
    fi
    save_proxy
}

json_escape() {
    local s="$1"
    s=${s//\\/\\\\}
    s=${s//\"/\\\"}
    s=${s//$'\n'/\\n}
    s=${s//$'\r'/\\r}
    s=${s//$'\t'/\\t}
    printf '"%s"' "${s}"
}

write_bootstrap_auth() {
    local user="$1" pass="$2"
    mkdir -p "${VAR_ROOT}"
    umask 077
    cat > "${BOOTSTRAP_AUTH_FILE}" <<EOF
{"username":$(json_escape "${user}"),"password":$(json_escape "${pass}")}
EOF
    chmod 0600 "${BOOTSTRAP_AUTH_FILE}"
}

ask_web_auth() {
    local user="" pass="" pass2="" reset="n"
    mkdir -p "${VAR_ROOT}"
    echo
    printf '%s\n' "${CYAN}${BOLD}设置网页登录账号${NC}"
    echo "  打开页面时需要输入这对账号密码"
    echo

    if [ -f "${AUTH_FILE}" ] || [ -f "${BOOTSTRAP_AUTH_FILE}" ]; then
        read_input "检测到已有登录账号，是否重新设置？[y/N] " reset || true
        case "${reset}" in
            y|Y|yes|YES) ;;
            *) log "保留现有网页登录账号"; return 0 ;;
        esac
    fi

    while true; do
        read_input "用户名: " user || true
        user="$(printf '%s' "${user}" | tr -d '[:space:]')"
        case "${user}" in
            "") warn "用户名不能为空" ;;
            *[!A-Za-z0-9._-]*) warn "用户名只能包含字母、数字、点、下划线和中划线" ;;
            *)
                if [ "${#user}" -lt 3 ] || [ "${#user}" -gt 32 ]; then
                    warn "用户名长度需要 3-32 位"
                else
                    break
                fi
                ;;
        esac
    done

    while true; do
        if [ -t 0 ]; then
            read -r -s -p "密码: " pass || true
            echo
            read -r -s -p "确认密码: " pass2 || true
            echo
        elif [ -r /dev/tty ]; then
            read -r -s -p "密码: " pass < /dev/tty || true
            echo > /dev/tty
            read -r -s -p "确认密码: " pass2 < /dev/tty || true
            echo > /dev/tty
        else
            die "无法交互输入密码，请在终端运行安装脚本"
        fi
        if [ -z "${pass}" ]; then
            warn "密码不能为空"
            continue
        fi
        if [ "${#pass}" -lt 8 ]; then
            warn "密码至少 8 位"
            continue
        fi
        if [ "${pass}" != "${pass2}" ]; then
            warn "两次密码不一致"
            continue
        fi
        break
    done

    write_bootstrap_auth "${user}" "${pass}"
    log "已保存网页登录账号: ${user}"
}

proxy_candidates() {
    local p
    if [ -n "${GH_PROXY}" ]; then
        printf '%s\n' "${GH_PROXY}"
    fi
    for p in "${PROXY_OPTIONS[@]}"; do
        [ "${p}" = "${GH_PROXY}" ] && continue
        printf '%s\n' "${p}"
    done
    printf '%s\n' ""
}

http_get() {
    local url="$1" proxy wrapped
    while IFS= read -r proxy; do
        if [ -n "${proxy}" ]; then
            wrapped="${proxy%/}/${url}"
        else
            wrapped="${url}"
        fi
        if curl -fsSL --connect-timeout 8 --max-time 30 "${wrapped}" 2>/dev/null; then
            return 0
        fi
    done < <(proxy_candidates)
    return 1
}

download_file() {
    local url="$1" dest="$2" proxy wrapped
    while IFS= read -r proxy; do
        if [ -n "${proxy}" ]; then
            wrapped="${proxy%/}/${url}"
        else
            wrapped="${url}"
        fi
        log "下载: ${wrapped}"
        if curl -fL --connect-timeout 8 --max-time 180 -o "${dest}" "${wrapped}"; then
            [ -s "${dest}" ] && return 0
        fi
        rm -f "${dest}"
        warn "下载失败，尝试下一个源"
    done < <(proxy_candidates)
    return 1
}

find_file() {
    local name="$1" p
    for p in \
        "${SRC_DIR}/${name}" \
        "${SRC_DIR}/frpc/app/app/${name}" \
        "${SRC_DIR}/frpc/app/ui/${name}" \
        "${APP_ROOT}/payload/${name}"
    do
        [ -f "${p}" ] || continue
        printf '%s' "${p}"
        return 0
    done
    return 1
}

find_dir() {
    local name="$1" p
    for p in \
        "${SRC_DIR}/${name}" \
        "${SRC_DIR}/frpc/app/ui/${name}" \
        "${APP_ROOT}/payload/${name}"
    do
        [ -d "${p}" ] || continue
        printf '%s' "${p}"
        return 0
    done
    return 1
}

local_payload_ok() {
    [ -f "${SRC_DIR}/main.go" ] && [ -f "${SRC_DIR}/auth.go" ] && [ -f "${SRC_DIR}/go.mod" ] && [ -f "${SRC_DIR}/restart.sh" ] && [ -f "${SRC_DIR}/statics/index.html" ] && [ -f "${SRC_DIR}/statics/login.html" ] && [ -f "${SRC_DIR}/statics/logo.png" ] && [ -f "${SRC_DIR}/statics/favicon.png" ]
}

ask_github_repo() {
    [ -n "${GITHUB_REPO}" ] || GITHUB_REPO="jiumian8/frpc-linux"
}

download_payload_from_github() {
    local archive extracted
    ask_github_repo
    PAYLOAD_TMP="$(mktemp -d)"
    archive="${PAYLOAD_TMP}/src.tgz"
    log "从 GitHub 下载源码: ${GITHUB_REPO}@${GITHUB_BRANCH}"
    if ! download_file "https://github.com/${GITHUB_REPO}/archive/refs/heads/${GITHUB_BRANCH}.tar.gz" "${archive}"; then
        if [ "${GITHUB_BRANCH}" != "master" ]; then
            warn "main 分支下载失败，尝试 master"
            download_file "https://github.com/${GITHUB_REPO}/archive/refs/heads/master.tar.gz" "${archive}" \
                || die "无法从 GitHub 下载仓库 ${GITHUB_REPO}"
        else
            die "无法从 GitHub 下载仓库 ${GITHUB_REPO}"
        fi
    fi
    tar -xzf "${archive}" -C "${PAYLOAD_TMP}"
    extracted="$(find "${PAYLOAD_TMP}" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
    [ -n "${extracted}" ] || die "源码压缩包解压失败"
    SRC_DIR="${extracted}"
}

prepare_payload() {
    if local_payload_ok; then
        log "使用本地源码: ${SRC_DIR}"
        return 0
    fi
    if [ -f "${APP_ROOT}/payload/main.go" ] && [ -d "${APP_ROOT}/payload/statics" ]; then
        SRC_DIR="${APP_ROOT}/payload"
        log "使用已安装的源码快照: ${SRC_DIR}"
        return 0
    fi
    download_payload_from_github
}

check_payload() {
    MAIN_GO="$(find_file main.go)" || die "缺少 main.go"
    GO_MOD="$(find_file go.mod)" || die "缺少 go.mod"
    RESTART_SH="$(find_file restart.sh)" || die "缺少 restart.sh"
    STATICS_DIR="$(find_dir statics)" || die "缺少 statics 目录"
    [ -f "${STATICS_DIR}/index.html" ] || die "statics/index.html 不存在"
    [ -f "${STATICS_DIR}/login.html" ] || die "statics/login.html 不存在"
    [ -f "${SRC_DIR}/auth.go" ] || die "缺少 auth.go"
    LOCAL_FRPC="$(find_file frpc || true)"
}

apt_install() {
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -y
    apt-get install -y --no-install-recommends "$@"
}

ensure_runtime_tools() {
    local need=()
    command -v curl >/dev/null 2>&1 || need+=(curl)
    command -v tar >/dev/null 2>&1 || need+=(tar)
    dpkg -s ca-certificates >/dev/null 2>&1 || need+=(ca-certificates)
    command -v ip >/dev/null 2>&1 || need+=(iproute2)
    if [ "${#need[@]}" -gt 0 ]; then
        log "安装依赖: ${need[*]}"
        apt_install "${need[@]}"
    fi
}

setup_go_env() {
    export HOME="/root"
    export USER="root"
    export XDG_CACHE_HOME="/root/.cache"
    export GOCACHE="${APP_ROOT}/build/.gocache"
    export GOPATH="${APP_ROOT}/build/.gopath"
    export GOTMPDIR="${APP_ROOT}/build/.gotmp"
    mkdir -p /root /root/.cache "${GOCACHE}" "${GOPATH}" "${GOTMPDIR}"
}

ensure_build_deps() {
    ensure_runtime_tools
    if ! command -v go >/dev/null 2>&1; then
        log "安装依赖: golang-go"
        apt_install golang-go
    fi
    command -v go >/dev/null 2>&1 || die "未找到 go，请先: apt-get install -y golang-go"
    setup_go_env
}

normalize_version() {
    printf '%s' "$1" | sed 's/^v//;s/^V//'
}

installed_frpc_version() {
    local bin="${1:-${FRPC_BIN}}" out=""
    [ -x "${bin}" ] || return 1
    out="$("${bin}" --version 2>/dev/null || true)"
    [ -n "${out}" ] || out="$("${bin}" -v 2>/dev/null || true)"
    printf '%s' "${out}" | grep -Eo '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -n 1
}

version_gt() {
    local left right
    left="$(normalize_version "$1")"
    right="$(normalize_version "$2")"
    [ "${left}" != "${right}" ] && [ "$(printf '%s\n%s\n' "${left}" "${right}" | sort -V | tail -n 1)" = "${left}" ]
}

latest_frpc_release() {
    local json tag loc
    json="$(http_get "https://api.github.com/repos/${FRP_REPO}/releases/latest" || true)"
    if [ -n "${json}" ]; then
        tag="$(printf '%s' "${json}" | grep -o '"tag_name": *"[^"]*"' | head -n 1 | sed 's/.*"tag_name": *"//;s/"$//')"
    fi
    if [ -z "${tag}" ]; then
        loc="$(curl -fsSLI --connect-timeout 8 --max-time 20 "$(github_url "https://github.com/${FRP_REPO}/releases/latest")" 2>/dev/null | awk 'tolower($1)=="location:" {print $2; exit}' | tr -d '\r')"
        tag="$(printf '%s' "${loc}" | grep -Eo 'v?[0-9]+\.[0-9]+(\.[0-9]+)?' | tail -n 1)"
    fi
    [ -n "${tag}" ] || return 1
    printf '%s https://github.com/%s/releases/download/%s/frp_%s_linux_amd64.tar.gz\n' \
        "${tag}" "${FRP_REPO}" "${tag}" "$(normalize_version "${tag}")"
}

fetch_frpc_to() {
    local dest="$1" latest_line tag asset tmpdir tarball extracted
    log "正在下载最新 frpc ..."
    latest_line="$(latest_frpc_release)" || die "无法获取 frpc 最新版本，请检查网络或换一个加速源"
    tag="$(printf '%s' "${latest_line}" | awk '{print $1}')"
    asset="$(printf '%s' "${latest_line}" | awk '{print $2}')"
    tmpdir="$(mktemp -d)"
    tarball="${tmpdir}/frp.tgz"
    if ! download_file "${asset}" "${tarball}"; then
        rm -rf "${tmpdir}"
        die "下载 frpc 失败: ${asset}"
    fi
    tar -xzf "${tarball}" -C "${tmpdir}"
    extracted="$(find "${tmpdir}" -type f -name frpc | head -n 1)"
    if [ -z "${extracted}" ]; then
        rm -rf "${tmpdir}"
        die "压缩包里没有 frpc"
    fi
    install -m 0755 "${extracted}" "${dest}"
    rm -rf "${tmpdir}"
    log "已下载 frpc $(normalize_version "${tag}")"
}

stop_existing() {
    systemctl stop "${SERVICE_NAME}" >/dev/null 2>&1 || true
    if [ -x "${UI_ROOT}/restart.sh" ]; then
        /bin/bash "${UI_ROOT}/restart.sh" stop-all >/dev/null 2>&1 || true
    fi
    pkill -f "${UI_ROOT}/index.cgi" >/dev/null 2>&1 || true
    pkill -f "${FRPC_BIN}" >/dev/null 2>&1 || true
}

copy_go_sources() {
    local dest="$1" f
    mkdir -p "${dest}"
    for f in "${SRC_DIR}"/*.go; do
        [ -f "${f}" ] || continue
        install -m 0644 "${f}" "${dest}/$(basename "${f}")"
    done
}

save_payload() {
    mkdir -p "${APP_ROOT}/payload"
    copy_go_sources "${APP_ROOT}/payload"
    install -m 0644 "${GO_MOD}" "${APP_ROOT}/payload/go.mod"
    install -m 0755 "${RESTART_SH}" "${APP_ROOT}/payload/restart.sh"
    if [ -f "${SCRIPT_PATH}" ] && [ -s "${SCRIPT_PATH}" ]; then
        install -m 0755 "${SCRIPT_PATH}" "${APP_ROOT}/payload/install.sh"
    fi
    rm -rf "${APP_ROOT}/payload/statics"
    cp -a "${STATICS_DIR}" "${APP_ROOT}/payload/statics"
    if [ -x "${FRPC_BIN}" ]; then
        install -m 0755 "${FRPC_BIN}" "${APP_ROOT}/payload/frpc"
    fi
}

install_files() {
    log "创建目录 ${APP_ROOT}"
    mkdir -p "${UI_ROOT}/statics" "${APP_BIN_DIR}" "${CONFIG_ROOT}/default" "${VAR_ROOT}/instances" "${VAR_ROOT}/config"

    if [ -n "${LOCAL_FRPC}" ]; then
        log "使用本地 frpc: ${LOCAL_FRPC}"
        install -m 0755 "${LOCAL_FRPC}" "${FRPC_BIN}"
    else
        fetch_frpc_to "${FRPC_BIN}"
    fi

    install -m 0755 "${RESTART_SH}" "${UI_ROOT}/restart.sh"

    log "复制网页文件"
    rm -rf "${UI_ROOT}/statics"
    cp -a "${STATICS_DIR}" "${UI_ROOT}/statics"
    find "${UI_ROOT}/statics" -type d -exec chmod 0755 {} \;
    find "${UI_ROOT}/statics" -type f -exec chmod 0644 {} \;

    if [ -f "${SCRIPT_PATH}" ] && [ -s "${SCRIPT_PATH}" ]; then
        install -m 0755 "${SCRIPT_PATH}" "${APP_ROOT}/install.sh"
        install -m 0755 "${SCRIPT_PATH}" /usr/local/sbin/frpc-web
    fi
    save_payload

    if [ ! -f "${CONFIG_ROOT}/default/frpc.toml" ]; then
        cat > "${CONFIG_ROOT}/default/frpc.toml" <<'EOF'
serverAddr = "127.0.0.1"
serverPort = 7000

# 不能删除, 否则连接失败会直接退出
loginFailExit = false
# 热重载配置，不能删除
webServer.addr = "127.0.0.1"
webServer.port = 7400
EOF
        chmod 0666 "${CONFIG_ROOT}/default/frpc.toml"
        cat > "${CONFIG_ROOT}/default/meta.json" <<'EOF'
{
  "name": "default",
  "enabled": true
}
EOF
        chmod 0666 "${CONFIG_ROOT}/default/meta.json"
    fi
}

compile_ui() {
    local gocache gopath gotmp
    log "编译 Web UI"
    gocache="${APP_ROOT}/build/.gocache"
    gopath="${APP_ROOT}/build/.gopath"
    gotmp="${APP_ROOT}/build/.gotmp"
    mkdir -p "${APP_ROOT}/build" "${UI_ROOT}" /root /root/.cache "${gocache}" "${gopath}" "${gotmp}"
    setup_go_env
    install -m 0644 "${GO_MOD}" "${APP_ROOT}/build/go.mod"
    copy_go_sources "${APP_ROOT}/build"
    cd "${APP_ROOT}/build"
    HOME=/root \
    USER=root \
    XDG_CACHE_HOME=/root/.cache \
    GOCACHE="${gocache}" \
    GOPATH="${gopath}" \
    GOTMPDIR="${gotmp}" \
    CGO_ENABLED=0 \
    GO111MODULE=on \
    GOPROXY=off \
    GOSUMDB=off \
    /usr/bin/env HOME=/root GOCACHE="${gocache}" GOPATH="${gopath}" GOTMPDIR="${gotmp}" XDG_CACHE_HOME=/root/.cache \
        go build -trimpath -ldflags '-s -w' -o "${UI_ROOT}/index.cgi" .
    chmod 0755 "${UI_ROOT}/index.cgi"
    [ -x "${UI_ROOT}/index.cgi" ] || die "UI 编译失败"
}

write_default_env() {
    cat > "${DEFAULT_FILE}" <<EOF
FRPC_UI_LISTEN=${UI_LISTEN}
FRPC_APPDEST=${TARGET_ROOT}
FRPC_UI_ROOT=${UI_ROOT}
FRPC_GH_PROXY=${GH_PROXY}
EOF
    chmod 0644 "${DEFAULT_FILE}"
}

write_systemd() {
    cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=frpc客户端
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=${UI_ROOT}
EnvironmentFile=-${DEFAULT_FILE}
Environment=FRPC_APPDEST=${TARGET_ROOT}
Environment=FRPC_UI_ROOT=${UI_ROOT}
Environment=FRPC_UI_LISTEN=${UI_LISTEN}
ExecStartPre=/bin/bash ${UI_ROOT}/restart.sh migrate-default
ExecStart=${UI_ROOT}/index.cgi
ExecStartPost=-/bin/bash ${UI_ROOT}/restart.sh start-all
ExecStop=-/bin/bash ${UI_ROOT}/restart.sh stop-all
Restart=on-failure
RestartSec=3
KillMode=mixed
TimeoutStartSec=60
TimeoutStopSec=30
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
    chmod 0644 "${SERVICE_FILE}"
}

open_firewall() {
    if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
        ufw allow "${UI_PORT}/tcp" comment "frpc-web" >/dev/null 2>&1 || true
        log "已在 ufw 放行 ${UI_PORT}/tcp"
    fi
}

host_ip() {
    local ip=""
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
    [ -n "${ip}" ] || ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    [ -n "${ip}" ] || ip="服务器IP"
    printf '%s' "${ip}"
}

port_in_use_by_others() {
    command -v ss >/dev/null 2>&1 || return 1
    systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null && return 1
    ss -lnt 2>/dev/null | grep -Eq ":${UI_PORT}([[:space:]]|$)"
}

wait_ready() {
    local i
    for i in $(seq 1 40); do
        if systemctl is-active --quiet "${SERVICE_NAME}"; then
            if command -v ss >/dev/null 2>&1 && ss -lnt 2>/dev/null | grep -Eq ":${UI_PORT}([[:space:]]|$)"; then
                return 0
            fi
        fi
        sleep 0.25
    done
    return 0
}

do_install() {
    require_root
    detect_os
    detect_arch
    choose_proxy
    ask_web_auth
    ensure_build_deps
    prepare_payload
    check_payload

    if port_in_use_by_others; then
        die "端口 ${UI_PORT} 已被占用，请先释放后再安装"
    fi

    log "开始安装 ${APP_NAME}"
    stop_existing
    install_files
    compile_ui
    write_default_env
    write_systemd
    open_firewall

    systemctl daemon-reload
    systemctl enable "${SERVICE_NAME}"
    systemctl restart "${SERVICE_NAME}"
    wait_ready

    if ! systemctl is-active --quiet "${SERVICE_NAME}"; then
        warn "服务未能正常启动，最近日志："
        journalctl -u "${SERVICE_NAME}" -n 40 --no-pager || true
        die "安装完成但服务启动失败"
    fi

    echo
    log "安装完成"
    printf '%s\n' "  访问地址:  ${CYAN}http://$(host_ip):${UI_PORT}${NC}"
    printf '%s\n' "  登录方式:  网页账号密码（安装时设置）"
    printf '%s\n' "  frpc版本:  $(installed_frpc_version "${FRPC_BIN}" || echo 未知)"
    printf '%s\n' "  加速源:    ${GH_PROXY:-GitHub 直连}"
    printf '%s\n' "  配置目录:  ${CONFIG_ROOT}"
    echo
}

keep_data_from_args() {
    local arg
    for arg in "$@"; do
        case "${arg}" in
            --purge|--delete-data) return 1 ;;
            --keep-data) return 0 ;;
        esac
    done
    if [ -t 0 ] || [ -r /dev/tty ]; then
        local answer=""
        read_input "是否同时删除配置和日志？[y/N] " answer || true
        case "${answer}" in
            y|Y|yes|YES) return 1 ;;
            *) return 0 ;;
        esac
    fi
    return 0
}

do_uninstall() {
    require_root
    log "开始卸载 ${APP_NAME}"
    systemctl stop "${SERVICE_NAME}" >/dev/null 2>&1 || true
    systemctl disable "${SERVICE_NAME}" >/dev/null 2>&1 || true
    if [ -x "${UI_ROOT}/restart.sh" ]; then
        /bin/bash "${UI_ROOT}/restart.sh" stop-all >/dev/null 2>&1 || true
    fi
    pkill -f "${UI_ROOT}/index.cgi" >/dev/null 2>&1 || true
    pkill -f "${FRPC_BIN}" >/dev/null 2>&1 || true
    rm -f "${SERVICE_FILE}" "${DEFAULT_FILE}" /usr/local/sbin/frpc-web
    systemctl daemon-reload >/dev/null 2>&1 || true
    systemctl reset-failed "${SERVICE_NAME}" >/dev/null 2>&1 || true

    if keep_data_from_args "$@"; then
        rm -rf "${TARGET_ROOT}" "${APP_ROOT}/build" "${APP_ROOT}/payload"
        log "已卸载程序，配置已保留: ${CONFIG_ROOT}"
    else
        rm -rf "${APP_ROOT}"
        log "已卸载程序、配置和日志"
    fi
    log "卸载完成"
}

do_update() {
    local current latest_line latest_tag latest_ver asset tmpdir tarball extracted
    require_root
    detect_arch
    choose_proxy
    ensure_runtime_tools
    [ -x "${FRPC_BIN}" ] || die "还没有安装，请先选 1 安装"

    current="$(installed_frpc_version "${FRPC_BIN}" || echo 未知)"
    log "当前 frpc 版本: ${current}"
    log "正在检测最新版本..."
    latest_line="$(latest_frpc_release)" || die "无法获取最新版本，请换一个加速源"
    latest_tag="$(printf '%s' "${latest_line}" | awk '{print $1}')"
    asset="$(printf '%s' "${latest_line}" | awk '{print $2}')"
    latest_ver="$(normalize_version "${latest_tag}")"
    echo "  最新版本: ${latest_ver}"

    if [ "${current}" != "未知" ] && ! version_gt "${latest_ver}" "${current}"; then
        log "已是最新版本，无需更新"
        return 0
    fi

    if [ -t 0 ] || [ -r /dev/tty ]; then
        local answer=""
        read_input "发现新版本 ${latest_ver}，是否更新？[Y/n] " answer || true
        case "${answer}" in
            n|N|no|NO) log "已取消更新"; return 0 ;;
        esac
    fi

    tmpdir="$(mktemp -d)"
    tarball="${tmpdir}/frp.tgz"
    trap 'rm -rf "${tmpdir}"; cleanup' RETURN
    log "下载 frpc ${latest_ver}"
    download_file "${asset}" "${tarball}" || die "下载失败"
    tar -xzf "${tarball}" -C "${tmpdir}"
    extracted="$(find "${tmpdir}" -type f -name frpc | head -n 1)"
    [ -n "${extracted}" ] || die "压缩包里没有 frpc"
    install -m 0755 "${extracted}" "${FRPC_BIN}"
    install -m 0755 "${extracted}" "${APP_ROOT}/payload/frpc" 2>/dev/null || true
    if [ -x "${UI_ROOT}/restart.sh" ]; then
        /bin/bash "${UI_ROOT}/restart.sh" stop-all >/dev/null 2>&1 || true
        /bin/bash "${UI_ROOT}/restart.sh" start-all >/dev/null 2>&1 || true
    fi
    systemctl restart "${SERVICE_NAME}" >/dev/null 2>&1 || true
    log "frpc 已更新到 $(installed_frpc_version "${FRPC_BIN}" || echo "${latest_ver}")"
}

show_menu() {
    local current="未安装"
    [ -x "${FRPC_BIN}" ] && current="$(installed_frpc_version "${FRPC_BIN}" || echo 已安装)"
    echo
    printf '%s\n' "${CYAN}${BOLD}================================${NC}"
    printf '%s\n' "${CYAN}${BOLD}        frpc客户端 管理脚本${NC}"
    printf '%s\n' "${CYAN}${BOLD}================================${NC}"
    echo "  当前版本: ${current}"
    echo "  访问地址: http://$(host_ip 2>/dev/null || echo 服务器IP):${UI_PORT}"
    echo
    echo "  1) 安装"
    echo "  2) 卸载"
    echo "  3) 检测更新"
    echo "  0) 退出"
    echo
}

run_menu() {
    local choice
    while true; do
        show_menu
        read_input "请输入选项 [0-3]: " choice || exit 0
        case "${choice}" in
            1) do_install; pause_if_interactive ;;
            2) do_uninstall; pause_if_interactive ;;
            3) do_update; pause_if_interactive ;;
            0|q|Q) exit 0 ;;
            *) warn "无效选项: ${choice}"; sleep 1 ;;
        esac
    done
}

usage() {
    cat <<EOF
用法:
  sudo bash $0          打开菜单：1 安装 / 2 卸载 / 3 检测更新
  sudo bash $0 1        安装（会先选择加速源和网页密码）
  sudo bash $0 2        卸载
  sudo bash $0 3        检测更新
  sudo bash $0 2 --purge

GitHub 一键安装:
  curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/jiumian8/frpc-linux/main/install.sh -o install.sh && sudo bash install.sh

加速源:
  1) https://gh-proxy.org
  2) https://v4.gh-proxy.org
  3) https://v6.gh-proxy.org
  4) https://cdn.gh-proxy.org
  5) https://axisnow.gh-proxy.org
  6) GitHub 直连

安装后打开: http://IP:${UI_PORT}
EOF
}

ACTION="${1:-}"
case "${ACTION}" in
    -h|--help|help) usage; exit 0 ;;
esac
require_root
case "${ACTION}" in
    "") run_menu ;;
    1|install) shift || true; do_install "$@" ;;
    2|uninstall|remove) shift || true; do_uninstall "$@" ;;
    3|update|check-update) shift || true; do_update "$@" ;;
    *) usage; die "未知参数: ${ACTION}" ;;
esac
