FROM ubuntu:22.04

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Asia/Shanghai

# 兼容 Ubuntu 22.04 的 sources.list 和较新镜像使用的 ubuntu.sources。
# AMD64 使用 ubuntu 仓库；ARM64 使用 ubuntu-ports 仓库。
RUN set -eux; \
    for file in /etc/apt/sources.list /etc/apt/sources.list.d/ubuntu.sources; do \
        if [ -f "$file" ]; then \
            sed -i \
                -e 's@http://archive.ubuntu.com/ubuntu@http://mirrors.ustc.edu.cn/ubuntu@g' \
                -e 's@http://security.ubuntu.com/ubuntu@http://mirrors.ustc.edu.cn/ubuntu@g' \
                -e 's@http://ports.ubuntu.com/ubuntu-ports@http://mirrors.ustc.edu.cn/ubuntu-ports@g' \
                "$file"; \
        fi; \
    done

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
    && rm -rf /var/lib/apt/lists/*

# 复制 spider 到 /usr/bin
COPY bin/spider /usr/bin/spider
RUN chmod +x /usr/bin/spider

# 继续使用既有配置挂载路径，避免影响部署。
RUN mkdir -p /etc/proxy

CMD ["/usr/bin/spider", "/etc/proxy/config.json"]
