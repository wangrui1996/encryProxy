#!/usr/bin/env bash
set -euo pipefail

# 统一镜像版本；也可通过 VERSION=v0.2.0 ./build.sh 覆盖。
VERSION="${VERSION:-v0.1.0}"

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_BUILD_DIR="${PROJECT_DIR}/build/cmd"
# 第一个参数仍用于指定镜像导出目录；默认 build/images。
# 默认只编译 Go；传入 --docker 才构建镜像并生成部署文件。
BUILD_DOCKER=0
OUTPUT_ARG="${1:-}"
if [[ "$OUTPUT_ARG" == "--docker" ]]; then
    BUILD_DOCKER=1
    OUTPUT_ARG=""
elif [[ "${2:-}" == "--docker" ]]; then
    BUILD_DOCKER=1
elif [[ -n "${2:-}" ]]; then
    echo "ERROR: 第二个参数只支持 --docker" >&2
    exit 1
fi
if [[ $# -gt 2 || ( $# -eq 2 && "${1:-}" == "--docker" ) ]]; then
    echo "用法: $0 [镜像输出目录] [--docker]，或 $0 --docker" >&2
    exit 1
fi
OUTPUT_DIR="${OUTPUT_ARG:-${PROJECT_DIR}/build/images}"
DEPLOY_DIR="${PROJECT_DIR}/build/deploy"
DEPLOY_SOURCE="${PROJECT_DIR}/docker/deploy"

[[ "$VERSION" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]*$ ]] || { echo "ERROR: 非法版本号: $VERSION" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "ERROR: 未找到 go" >&2; exit 1; }
mkdir -p "$GO_BUILD_DIR"

if [[ "$BUILD_DOCKER" -eq 1 ]]; then
    for file in docker-compose.yml env.example setup-host.sh; do
        [[ -f "${DEPLOY_SOURCE}/${file}" ]] || { echo "ERROR: 找不到部署文件: ${DEPLOY_SOURCE}/${file}" >&2; exit 1; }
    done

    mkdir -p "$OUTPUT_DIR" "$DEPLOY_DIR"
    OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"

    command -v docker >/dev/null 2>&1 || { echo "ERROR: 未找到 docker CLI" >&2; exit 1; }
    docker info >/dev/null 2>&1 || { echo "ERROR: 无法连接 Docker Engine；在容器内运行时需要 docker.sock 和访问权限" >&2; exit 1; }
    docker buildx version >/dev/null 2>&1 || { echo "ERROR: 未安装 Docker Buildx" >&2; exit 1; }

    # 临时目录存放与 Dockerfile 的 COPY bin/spider 对应的二进制。
    TMP_DIR="$(mktemp -d "${OUTPUT_DIR}/.build-XXXXXX")"
    trap 'rm -rf "$TMP_DIR"' EXIT
    mkdir -p "$TMP_DIR/spider/bin"
fi
cd "$PROJECT_DIR"

build_image() {
    local name="$1" platform="$2" suffix="$3" context="$4" dockerfile="$5"
    local tag="${name}:${VERSION}-linux-${suffix}"
    local tar_path="${OUTPUT_DIR}/${name}-${VERSION}-linux-${suffix}.tar"
    local temp_tar="${TMP_DIR}/${name}-${suffix}.tar"
    local -a proxy_args=()

    # 若调用方设置 HTTP(S)_PROXY，则传递给 Dockerfile 的 RUN 命令。
    [[ -n "${HTTP_PROXY:-}" ]] && proxy_args+=(--build-arg "HTTP_PROXY=${HTTP_PROXY}")
    [[ -n "${HTTPS_PROXY:-}" ]] && proxy_args+=(--build-arg "HTTPS_PROXY=${HTTPS_PROXY}")
    [[ -n "${NO_PROXY:-}" ]] && proxy_args+=(--build-arg "NO_PROXY=${NO_PROXY}")

    echo "==> Docker: ${tag} (${platform})"
    docker buildx build --platform "$platform" --file "$dockerfile" \
        --tag "$tag" "${proxy_args[@]}" \
        --output "type=docker,dest=${temp_tar}" "$context"
    mv -f "$temp_tar" "$tar_path"
    echo "    Saved: ${tar_path}"
}

prepare_deploy() {
    local suffix="$1"
    local target="${DEPLOY_DIR}/linux-${suffix}"

    mkdir -p "$target/config"
    cp "${DEPLOY_SOURCE}/docker-compose.yml" "$target/docker-compose.yml"
    cp "${DEPLOY_SOURCE}/setup-host.sh" "$target/setup-host.sh"
    chmod +x "$target/setup-host.sh"

    # 配置文件若存在则复制；不覆盖部署目录中已经调整过的配置。
    # 清理历史构建产物中不再使用的 SmartDNS 配置目录。
    rm -rf -- "$target/config/smartdns"
    if [[ -d "${DEPLOY_SOURCE}/config" ]]; then
        local item
        for item in "${DEPLOY_SOURCE}"/config/* "${DEPLOY_SOURCE}"/config/.[!.]* "${DEPLOY_SOURCE}"/config/..?*; do
            [[ -e "$item" || -L "$item" ]] || continue
            [[ "${item##*/}" == "smartdns" ]] && continue
            cp -a -n "$item" "$target/config/"
        done
    fi

    # 只生成环境配置模板；部署者修改后手动 mv env.example .env。
    # 不自动创建或覆盖已有的 .env，避免覆盖用户部署配置。
    {
        printf 'IMAGE_VERSION=%s\nIMAGE_ARCH=%s\n' "$VERSION" "$suffix"
        cat "${DEPLOY_SOURCE}/env.example"
    } > "$target/env.example"
    # 清理旧版构建生成的隐藏模板，避免两个模板同时存在。
    rm -f -- "$target/.env.example"

    echo "==> Deploy: ${target}"
    [[ -f "$target/config/config.json" ]] || echo "    NOTICE: 部署前请提供 config/config.json"
}

for arch in amd64 arm64; do
    case "$arch" in
        amd64) suffix=x64 ;;
        arm64) suffix=arm64 ;;
    esac

    binary="${GO_BUILD_DIR}/spider-linux-${suffix}"
    echo "==> Go: linux/${arch} -> ${binary}"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -buildvcs=false -o "$binary" ./cmd

    if [[ "$BUILD_DOCKER" -eq 1 ]]; then
        cp "$binary" "$TMP_DIR/spider/bin/spider"
        build_image spider "linux/${arch}" "$suffix" \
            "$TMP_DIR/spider" "${PROJECT_DIR}/docker/spider/Dockerfile"

        build_image netfilter "linux/${arch}" "$suffix" \
            "${PROJECT_DIR}/docker/netfilter" "${PROJECT_DIR}/docker/netfilter/Dockerfile"

        prepare_deploy "$suffix"
    fi
done

# 默认模式才编译 Windows 可执行文件；Docker 模式只构建 Linux 两个架构。
if [[ "$BUILD_DOCKER" -eq 0 ]]; then
    for arch in amd64 arm64; do
        case "$arch" in
            amd64) suffix=x64 ;;
            arm64) suffix=arm64 ;;
        esac

        binary="${GO_BUILD_DIR}/spider-windows-${suffix}.exe"
        echo "==> Go: windows/${arch} -> ${binary}"
        CGO_ENABLED=0 GOOS=windows GOARCH="$arch" go build -buildvcs=false -o "$binary" ./cmd
    done
fi

if [[ "$BUILD_DOCKER" -eq 1 ]]; then
    echo "==> 构建完成：Go ${GO_BUILD_DIR}；镜像 ${OUTPUT_DIR}；部署文件 ${DEPLOY_DIR}"
else
    echo "==> Go 编译完成：${GO_BUILD_DIR}（未构建 Docker 镜像）"
fi
