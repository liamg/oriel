# Oriel's crit sandbox: crit's base image plus Gio's Linux build deps, Xvfb and
# Mesa for headless renders, and the pinned nvim the vim oracle needs.
FROM debian:bookworm-slim
ARG MISE_VERSION=v2026.6.10
ARG NVIM_VERSION=v0.11.7
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git ca-certificates curl wget ripgrep openssh-client less procps jq make gcc g++ libc6-dev \
      python3 unzip xz-utils zip tar file tree \
      chromium-headless-shell fonts-liberation \
      pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-dev libxkbcommon-x11-dev \
      libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libxfixes-dev libvulkan-dev \
      libgl1-mesa-dri mesa-vulkan-drivers xvfb xauth imagemagick \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL "https://github.com/jdx/mise/releases/download/${MISE_VERSION}/install.sh" | MISE_INSTALL_PATH=/usr/local/bin/mise sh \
 && case "$(dpkg --print-architecture)" in \
      amd64) nvim=nvim-linux-x86_64; sum=38a7c6317f94503841096c00e8fde05ef04b9472fc9d7d62b6e033cecd6f7991 ;; \
      arm64) nvim=nvim-linux-arm64; sum=99bb3c53604e83ce18fc0b459e34cf1a5e212f4e5fbe2eb136b3c18092ae9905 ;; \
    esac \
 && cd /tmp \
 && curl -fsSLo nvim.tar.gz "https://github.com/neovim/neovim/releases/download/${NVIM_VERSION}/${nvim}.tar.gz" \
 && echo "${sum}  nvim.tar.gz" | sha256sum -c - \
 && tar -xzf nvim.tar.gz -C /opt \
 && ln -s "/opt/${nvim}/bin/nvim" /usr/local/bin/nvim \
 && rm nvim.tar.gz
ENV ORIEL_NVIM=/usr/local/bin/nvim
