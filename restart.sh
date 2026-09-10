#! /bin/bash

APP_ROOT=/var/apps/frpc
VAR_ROOT=/var/apps/frpc/var
CONFIG_ROOT=/var/apps/frpc/shares/frpc
TARGET_ROOT=/var/apps/frpc/target
SVC_CWD=/var/apps/frpc/target
FRPC_BIN=/var/apps/frpc/target/app/frpc
MANAGER_LOG=/var/apps/frpc/var/manager.log
LOCK_DIR=/var/apps/frpc/var/manager.lock
LOCK_OWNER_FILE=/var/apps/frpc/var/manager.lock/pid
RUNTIME_INSTANCES_ROOT=/var/apps/frpc/var/instances
DEFAULT_INSTANCE_ID=default
LEGACY_CONFIG_FILE=/var/apps/frpc/var/config/frpc.toml
LEGACY_CONFIG_BACKUP=/var/apps/frpc/var/config/frpc.toml.bak
LEGACY_PID_FILE=/var/apps/frpc/var/frpc.pid
LEGACY_LOG_FILE=/var/apps/frpc/var/frpc.log
MODE="${1:-restart}"
INSTANCE_ID="${2:-${DEFAULT_INSTANCE_ID}}"

log_msg() {
    mkdir -p "${VAR_ROOT}" "${CONFIG_ROOT}" "${RUNTIME_INSTANCES_ROOT}" 2>/dev/null || true
    printf '%s - %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$1" >> "${MANAGER_LOG}"
}

instance_log_msg() {
    local instance_id=$1
    local message=$2
    local log_file

    log_file=$(instance_log_file "${instance_id}")
    mkdir -p "$(instance_runtime_dir "${instance_id}")" 2>/dev/null || true
    printf '%s - [manager] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "${message}" >> "${log_file}" 2>/dev/null || true
    log_msg "instance [${instance_id}] ${message}"
}

ensure_base_dirs() {
    mkdir -p "${VAR_ROOT}" "${CONFIG_ROOT}" "${RUNTIME_INSTANCES_ROOT}" || return 1
}

instance_config_dir() {
    printf '%s/%s\n' "${CONFIG_ROOT}" "$1"
}

instance_runtime_dir() {
    printf '%s/%s\n' "${RUNTIME_INSTANCES_ROOT}" "$1"
}

legacy_instance_config() {
    printf '%s/frpc.toml\n' "$(instance_runtime_dir "$1")"
}

legacy_instance_meta_file() {
    printf '%s/meta.json\n' "$(instance_runtime_dir "$1")"
}

instance_config() {
    local config_file legacy_config_file instance_id

    instance_id=$1
    config_file="$(instance_config_dir "${instance_id}")/frpc.toml"
    legacy_config_file="$(legacy_instance_config "${instance_id}")"
    if [ -f "${config_file}" ]; then
        printf '%s\n' "${config_file}"
        return
    fi
    if [ -f "${legacy_config_file}" ]; then
        printf '%s\n' "${legacy_config_file}"
        return
    fi
    if [ "${instance_id}" = "${DEFAULT_INSTANCE_ID}" ] && [ -f "${LEGACY_CONFIG_FILE}" ]; then
        printf '%s\n' "${LEGACY_CONFIG_FILE}"
        return
    fi
    printf '%s\n' "${config_file}"
}

instance_pid_file() {
    printf '%s/frpc.pid\n' "$(instance_runtime_dir "$1")"
}

instance_log_file() {
    printf '%s/frpc.log\n' "$(instance_runtime_dir "$1")"
}

instance_meta_file() {
    local meta_file legacy_meta_file

    meta_file="$(instance_config_dir "$1")/meta.json"
    legacy_meta_file="$(legacy_instance_meta_file "$1")"
    if [ -f "${meta_file}" ]; then
        printf '%s\n' "${meta_file}"
        return
    fi
    if [ -f "${legacy_meta_file}" ]; then
        printf '%s\n' "${legacy_meta_file}"
        return
    fi
    printf '%s\n' "${meta_file}"
}

instance_has_connected_status() {
    local log_file=$1

    if [ ! -f "${log_file}" ]; then
        return 1
    fi

    awk '
        {
            line = tolower($0)
            if (index(line, "login to server success") > 0 || index(line, "[manager] hot reload preserved connected state") > 0) {
                state = "connected"
            } else if (
                index(line, "token in login doesn'\''t match token") > 0 ||
                index(line, "connect to server error") > 0 ||
                index(line, "[manager] restart requested") > 0 ||
                index(line, "[manager] start requested") > 0 ||
                index(line, "[manager] starting frpc") > 0 ||
                index(line, "[manager] trying hot reload") > 0 ||
                index(line, "[manager] hot reload succeeded") > 0 ||
                index(line, "[manager] hot reload failed") > 0 ||
                index(line, "[manager] not running, fallback to restart") > 0 ||
                index(line, "[manager] stop requested") > 0 ||
                index(line, "failed to stay running after start") > 0
            ) {
                state = "other"
            }
        }
        END {
            exit(state == "connected" ? 0 : 1)
        }
    ' "${log_file}"
}

read_pid() {
    local pid_file=$1
    if [ -r "${pid_file}" ]; then
        head -n 1 "${pid_file}" | tr -d '[:space:]'
    fi
}

check_process() {
    local pid=$1
    if [ -z "${pid}" ]; then
        return 1
    fi
    if kill -0 "${pid}" 2>/dev/null && [ -d "/proc/${pid}" ]; then
        return 0
    fi
    return 1
}

pid_matches_config() {
    local pid=$1
    local config_file=$2
    local cmdline

    if ! check_process "${pid}"; then
        return 1
    fi

    cmdline=$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null || true)
    if printf '%s' "${cmdline}" | grep -F -- "${FRPC_BIN}" >/dev/null 2>&1 && \
        printf '%s' "${cmdline}" | grep -F -- "${config_file}" >/dev/null 2>&1; then
        return 0
    fi
    return 1
}

find_pids_by_config() {
    local config_file=$1
    local proc pid cmdline

    for proc in /proc/[0-9]*; do
        [ -r "${proc}/cmdline" ] || continue
        pid=$(basename "${proc}")
        cmdline=$(tr '\0' ' ' < "${proc}/cmdline" 2>/dev/null || true)
        if printf '%s' "${cmdline}" | grep -F -- "${FRPC_BIN}" >/dev/null 2>&1 && \
            printf '%s' "${cmdline}" | grep -F -- "${config_file}" >/dev/null 2>&1; then
            printf '%s\n' "${pid}"
        fi
    done
}

stop_pid() {
    local pid=$1
    local count=0

    if ! check_process "${pid}"; then
        return 0
    fi

    kill -TERM "${pid}" 2>/dev/null || true
    while check_process "${pid}" && [ ${count} -lt 5 ]; do
        sleep 1
        count=$((count + 1))
    done

    if check_process "${pid}"; then
        kill -KILL "${pid}" 2>/dev/null || true
        sleep 0.5
    fi
}

verify_config() {
    local instance_id=$1
    local config_file=$2
    local log_file=$3
    local output
    local status

    if [ ! -x "${FRPC_BIN}" ]; then
        instance_log_msg "${instance_id}" "start skipped: frpc binary not found (${FRPC_BIN})"
        return 1
    fi

    if [ ! -f "${config_file}" ]; then
        instance_log_msg "${instance_id}" "start skipped: config file not found (${config_file})"
        return 1
    fi

    output=$("${FRPC_BIN}" verify -c "${config_file}" 2>&1)
    status=$?
    if [ ${status} -eq 0 ]; then
        return 0
    fi

    case "${output}" in
        *"unknown command"*|*"No help topic for 'verify'"*)
            printf '%s\n' "${output}" >> "${log_file}"
            instance_log_msg "${instance_id}" "verify unsupported, continue starting"
            return 0
            ;;
    esac

    printf '%s\n' "${output}" >> "${log_file}"
    instance_log_msg "${instance_id}" "verify failed, see frpc output above"
    return 1
}

acquire_lock() {
    local count=0
    local owner_pid
    ensure_base_dirs || return 1

    while ! mkdir "${LOCK_DIR}" 2>/dev/null; do
        owner_pid=$(read_pid "${LOCK_OWNER_FILE}")
        if [ -z "${owner_pid}" ] || ! check_process "${owner_pid}"; then
            log_msg "stale manager lock detected, removing"
            rm -f "${LOCK_OWNER_FILE}" 2>/dev/null || true
            rmdir "${LOCK_DIR}" 2>/dev/null || true
            sleep 0.1
            continue
        fi
        count=$((count + 1))
        if [ ${count} -ge 30 ]; then
            log_msg "failed to acquire manager lock"
            return 1
        fi
        sleep 0.2
    done

    printf '%s\n' "$$" > "${LOCK_OWNER_FILE}"
    trap 'release_lock' EXIT INT TERM
    return 0
}

release_lock() {
    rm -f "${LOCK_OWNER_FILE}" 2>/dev/null || true
    rmdir "${LOCK_DIR}" 2>/dev/null || true
    trap - EXIT INT TERM
}

ensure_default_instance_migrated() {
    local default_dir default_config source_config

    ensure_base_dirs || return 1
    default_dir=$(instance_config_dir "${DEFAULT_INSTANCE_ID}")
    default_config="$(instance_config_dir "${DEFAULT_INSTANCE_ID}")/frpc.toml"

    if [ -f "${default_config}" ]; then
        return 0
    fi

    source_config="$(legacy_instance_config "${DEFAULT_INSTANCE_ID}")"
    if [ ! -f "${source_config}" ]; then
        source_config="${LEGACY_CONFIG_FILE}"
    fi
    if [ ! -f "${source_config}" ]; then
        return 0
    fi

    mkdir -p "${default_dir}" || return 1
    cp "${source_config}" "${default_config}" || return 1
    if [ "${source_config}" = "${LEGACY_CONFIG_FILE}" ] && [ ! -f "${LEGACY_CONFIG_BACKUP}" ]; then
        cp "${LEGACY_CONFIG_FILE}" "${LEGACY_CONFIG_BACKUP}" 2>/dev/null || true
    fi
    instance_log_msg "${DEFAULT_INSTANCE_ID}" "legacy config migrated from ${source_config} to ${default_config}"
}

stop_legacy_default() {
    local pid

    instance_log_msg "${DEFAULT_INSTANCE_ID}" "checking legacy default process pid=${LEGACY_PID_FILE} log=${LEGACY_LOG_FILE}"
    pid=$(read_pid "${LEGACY_PID_FILE}")
    if [ -n "${pid}" ] && pid_matches_config "${pid}" "${LEGACY_CONFIG_FILE}"; then
        instance_log_msg "${DEFAULT_INSTANCE_ID}" "stopping legacy default frpc pid=${pid}"
        stop_pid "${pid}"
    fi

    for pid in $(find_pids_by_config "${LEGACY_CONFIG_FILE}"); do
        instance_log_msg "${DEFAULT_INSTANCE_ID}" "stopping discovered legacy default frpc pid=${pid}"
        stop_pid "${pid}"
    done

    rm -f "${LEGACY_PID_FILE}" 2>/dev/null || true
    instance_log_msg "${DEFAULT_INSTANCE_ID}" "legacy default pid file cleared: ${LEGACY_PID_FILE}"
}

list_instance_ids() {
    local dir

    ensure_base_dirs || return 1
    {
        for dir in "${CONFIG_ROOT}"/*; do
            [ -d "${dir}" ] || continue
            basename "${dir}"
        done
        for dir in "${RUNTIME_INSTANCES_ROOT}"/*; do
            [ -d "${dir}" ] || continue
            if [ -f "${dir}/frpc.toml" ] || [ -f "${dir}/meta.json" ]; then
                basename "${dir}"
            fi
        done
    } | awk 'NF && !seen[$0]++'
}

instance_enabled() {
    local instance_id=$1
    local meta_file

    meta_file=$(instance_meta_file "${instance_id}")
    if [ ! -f "${meta_file}" ]; then
        return 0
    fi

    if grep -Eq '"enabled"[[:space:]]*:[[:space:]]*false' "${meta_file}"; then
        return 1
    fi
    return 0
}

status_instance() {
    local instance_id=$1
    local config_file pid_file pid

    config_file=$(instance_config "${instance_id}")
    pid_file=$(instance_pid_file "${instance_id}")
    pid=$(read_pid "${pid_file}")

    if [ -n "${pid}" ] && pid_matches_config "${pid}" "${config_file}"; then
        return 0
    fi

    for pid in $(find_pids_by_config "${config_file}"); do
        printf '%s' "${pid}" > "${pid_file}"
        return 0
    done

    rm -f "${pid_file}" 2>/dev/null || true
    return 1
}

start_instance() {
    local instance_id=$1
    local config_file pid_file log_file old_pid new_pid

    if ! instance_enabled "${instance_id}"; then
        log_msg "instance [${instance_id}] disabled, skip start and ensure stopped"
        stop_instance "${instance_id}" || return 1
        return 0
    fi

    config_file=$(instance_config "${instance_id}")
    pid_file=$(instance_pid_file "${instance_id}")
    log_file=$(instance_log_file "${instance_id}")
    mkdir -p "$(instance_runtime_dir "${instance_id}")" || return 1
    : >> "${log_file}"
    instance_log_msg "${instance_id}" "start requested config=${config_file} pid=${pid_file} log=${log_file}"

    if ! verify_config "${instance_id}" "${config_file}" "${log_file}"; then
        return 1
    fi

    old_pid=$(read_pid "${pid_file}")
    if [ -n "${old_pid}" ] && pid_matches_config "${old_pid}" "${config_file}"; then
        instance_log_msg "${instance_id}" "already running pid=${old_pid}"
        return 0
    fi

    rm -f "${pid_file}" 2>/dev/null || true
    if [ "${instance_id}" = "${DEFAULT_INSTANCE_ID}" ]; then
        stop_legacy_default
    fi

    if [ -n "${SVC_CWD}" ]; then
        cd "${SVC_CWD}" || true
    fi

    instance_log_msg "${instance_id}" "starting frpc binary=${FRPC_BIN}"
    nohup setsid "${FRPC_BIN}" -c "${config_file}" >> "${log_file}" 2>&1 < /dev/null &
    new_pid=$!
    sleep 0.5

    if pid_matches_config "${new_pid}" "${config_file}"; then
        printf '%s' "${new_pid}" > "${pid_file}"
        instance_log_msg "${instance_id}" "started pid=${new_pid}"
        return 0
    fi

    instance_log_msg "${instance_id}" "failed to stay running after start, pid file not written"
    return 1
}

stop_instance() {
    local instance_id=$1
    local config_file pid_file pid

    config_file=$(instance_config "${instance_id}")
    pid_file=$(instance_pid_file "${instance_id}")
    pid=$(read_pid "${pid_file}")

    instance_log_msg "${instance_id}" "stop requested config=${config_file} pid=${pid_file}"

    if [ -n "${pid}" ] && pid_matches_config "${pid}" "${config_file}"; then
        instance_log_msg "${instance_id}" "stopping pid=${pid}"
        stop_pid "${pid}"
    fi

    for pid in $(find_pids_by_config "${config_file}"); do
        instance_log_msg "${instance_id}" "stopping discovered pid=${pid}"
        stop_pid "${pid}"
    done

    rm -f "${pid_file}" 2>/dev/null || true
    instance_log_msg "${instance_id}" "pid file cleared: ${pid_file}"
    return 0
}

restart_instance() {
    local instance_id=$1
    instance_log_msg "${instance_id}" "restart requested"
    stop_instance "${instance_id}" || return 1
    start_instance "${instance_id}"
}

reload_instance() {
    local instance_id=$1
    local config_file pid_file log_file pid was_connected=0

    if ! instance_enabled "${instance_id}"; then
        instance_log_msg "${instance_id}" "disabled, skip reload and ensure stopped"
        stop_instance "${instance_id}" || return 1
        return 0
    fi

    config_file=$(instance_config "${instance_id}")
    pid_file=$(instance_pid_file "${instance_id}")
    log_file=$(instance_log_file "${instance_id}")
    pid=$(read_pid "${pid_file}")

    if ! verify_config "${instance_id}" "${config_file}" "${log_file}"; then
        return 1
    fi

    if [ -n "${pid}" ] && pid_matches_config "${pid}" "${config_file}"; then
        if instance_has_connected_status "${log_file}"; then
            was_connected=1
        fi
        if [ -n "${SVC_CWD}" ]; then
            cd "${SVC_CWD}" || true
        fi

        instance_log_msg "${instance_id}" "trying hot reload"
        if "${FRPC_BIN}" reload -c "${config_file}" >> "${log_file}" 2>&1; then
            if [ "${was_connected}" -eq 1 ]; then
                instance_log_msg "${instance_id}" "hot reload preserved connected state"
            else
                instance_log_msg "${instance_id}" "hot reload succeeded"
            fi
            return 0
        fi

        instance_log_msg "${instance_id}" "hot reload failed, fallback to restart"
    else
        instance_log_msg "${instance_id}" "not running, fallback to restart"
    fi

    restart_instance "${instance_id}"
}

start_all() {
    local instance_id
    local rc=0

    ensure_default_instance_migrated || rc=1
    for instance_id in $(list_instance_ids); do
        if ! instance_enabled "${instance_id}"; then
            log_msg "instance [${instance_id}] disabled, skip start"
            continue
        fi
        start_instance "${instance_id}" || rc=1
    done
    return ${rc}
}

stop_all() {
    local instance_id
    local rc=0

    for instance_id in $(list_instance_ids); do
        stop_instance "${instance_id}" || rc=1
    done
    stop_legacy_default || rc=1
    return ${rc}
}

status_all() {
    local instance_id

    ensure_default_instance_migrated || return 3
    for instance_id in $(list_instance_ids); do
        if ! instance_enabled "${instance_id}"; then
            continue
        fi
        if ! status_instance "${instance_id}"; then
            return 3
        fi
    done
    return 0
}

if [ "${MODE}" != "status-all" ] && [ "${MODE}" != "status-one" ]; then
    log_msg "${MODE} request received"
fi

case "${MODE}" in
    start-all)
        acquire_lock && start_all
        exit $?
        ;;
    stop-all)
        acquire_lock && stop_all
        exit $?
        ;;
    status-all)
        if status_all; then
            exit 0
        fi
        exit 3
        ;;
    start-one)
        acquire_lock && start_instance "${INSTANCE_ID}"
        exit $?
        ;;
    stop-one)
        acquire_lock && stop_instance "${INSTANCE_ID}"
        exit $?
        ;;
    restart-one)
        acquire_lock && restart_instance "${INSTANCE_ID}"
        exit $?
        ;;
    reload-one)
        acquire_lock && reload_instance "${INSTANCE_ID}"
        exit $?
        ;;
    status-one)
        if status_instance "${INSTANCE_ID}"; then
            exit 0
        fi
        exit 3
        ;;
    migrate-default)
        acquire_lock && ensure_default_instance_migrated
        exit $?
        ;;
    restart)
        acquire_lock && restart_instance "${DEFAULT_INSTANCE_ID}"
        exit $?
        ;;
    reload)
        acquire_lock && reload_instance "${DEFAULT_INSTANCE_ID}"
        exit $?
        ;;
    *)
        exit 1
        ;;
esac
