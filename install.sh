#!/usr/bin/env bash
set -u -o pipefail

REPOSITORY='https://github.com/ilvar/strictgo'

error() {
    printf 'strictgo installer: %s\n' "${1}" >&2 || return 1
}

target_for_host() {
    case "${1}:${2}" in
        Linux:x86_64|Linux:amd64) printf 'linux-amd64' ;;
        Linux:aarch64|Linux:arm64) printf 'linux-arm64' ;;
        Darwin:x86_64|Darwin:amd64) printf 'darwin-amd64' ;;
        Darwin:aarch64|Darwin:arm64) printf 'darwin-arm64' ;;
        *) printf '' ;;
    esac
}

verify_checksum() {
    if sha256sum --version >/dev/null 2>&1; then
        sha256sum --check --strict "${1}" || return 1
    else
        shasum -a 256 --check "${1}" || return 1
    fi
}

install_release() {
    local workdir install_dir os arch target version latest_url tag binary asset base_url
    workdir="${1}"
    if [[ -z "${STRICTGO_INSTALL_DIR:-}" && -z "${HOME:-}" ]]; then
        error 'set HOME or STRICTGO_INSTALL_DIR to choose a destination' || return 1
        return 1
    fi
    install_dir="${STRICTGO_INSTALL_DIR:-${HOME}/.local/bin}"
    os=$(uname -s) || return 1
    arch=$(uname -m) || return 1
    target=$(target_for_host "${os}" "${arch}") || return 1
    version="${STRICTGO_VERSION:-latest}"
    if [[ "${version}" == latest ]]; then
        latest_url=$(curl --fail --silent --show-error --location --retry 3 --connect-timeout 10 --max-time 60 \
            --proto '=https' --proto-redir '=https' --output /dev/null --write-out '%{url_effective}' \
            "${REPOSITORY}/releases/latest") || return 1
        tag="${latest_url##*/}"
    else
        tag="v${version#v}"
    fi
    if [[ ! "${tag}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        error 'STRICTGO_VERSION must be latest or a version such as 0.1.2 or v0.1.2; the resolved release must use that tag format' || return 1
        return 1
    fi

    if [[ -n "${target}" && "${STRICTGO_BUILD_FROM_SOURCE:-0}" == 0 ]]; then
        asset="strictgo-${target}"
        base_url="${REPOSITORY}/releases/download/${tag}"
        curl --fail --silent --show-error --location --retry 3 --connect-timeout 10 --max-time 120 \
            --proto '=https' --proto-redir '=https' --output "${workdir}/${asset}" \
            "${base_url}/${asset}" || return 1
        curl --fail --silent --show-error --location --retry 3 --connect-timeout 10 --max-time 60 \
            --proto '=https' --proto-redir '=https' --output "${workdir}/${asset}.sha256" \
            "${base_url}/${asset}.sha256" || return 1
        # The checksum's filename is relative to the isolated download directory.
        if (cd "${workdir}" && verify_checksum "${asset}.sha256" || exit 1); then
            binary="${workdir}/${asset}"
        else
            error 'checksum verification failed; refusing to install' || return 1
            return 1
        fi
    else
        printf 'Building strictgo %s for %s/%s from source.\n' "${tag}" "${os}" "${arch}"
        if go version >/dev/null 2>&1; then
            mkdir -p -- "${workdir}/build/bin" || return 1
            GOBIN="${workdir}/build/bin" GOTOOLCHAIN=go1.26.0 go install \
                "github.com/ilvar/strictgo@${tag}" || return 1
            binary="${workdir}/build/bin/strictgo"
        else
            error 'source builds require Go (1.21+ with toolchain downloads, or Go 1.26+); install Go from https://go.dev/dl first' || return 1
            return 1
        fi
    fi

    mkdir -p -- "${install_dir}" || return 1
    install -m 0755 -- "${binary}" "${install_dir}/strictgo" || return 1
    printf 'Installed strictgo %s to %s/strictgo\n' "${tag}" "${install_dir}"
    printf 'Ensure %s is on PATH.\n' "${install_dir}"
    printf 'Runtime dependencies: Go 1.26+.\n'
}

main() {
    local workdir result
    if [[ "${#}" -ne 0 ]]; then
        error 'usage: bash install.sh (configure STRICTGO_VERSION, STRICTGO_INSTALL_DIR or STRICTGO_BUILD_FROM_SOURCE=1 via environment)' || return 1
        return 1
    fi
    workdir=$(mktemp -d "${TMPDIR:-/tmp}/strictgo-install.XXXXXXXX") || return 1
    if install_release "${workdir}"; then
        result=0
    else
        result=1
    fi
    rm -rf -- "${workdir}" || return 1
    return "${result}"
}

main "${@}" || exit 1
