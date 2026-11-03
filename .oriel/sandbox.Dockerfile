# Oriel's own sandbox steps, on Oriel's base image: Gio's Linux build deps, Xvfb and Mesa for headless renders,
# the pinned nvim the vim oracle needs, and wasi-sdk to build the tree-sitter grammars to WASM from source.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-dev libxkbcommon-x11-dev \
      libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libxfixes-dev libvulkan-dev \
      libgl1-mesa-dri mesa-vulkan-drivers xvfb xauth imagemagick \
 && rm -rf /var/lib/apt/lists/*
RUN case "$(dpkg --print-architecture)" in \
      amd64) nvim=nvim-linux-x86_64; sum=38a7c6317f94503841096c00e8fde05ef04b9472fc9d7d62b6e033cecd6f7991 ;; \
      arm64) nvim=nvim-linux-arm64; sum=99bb3c53604e83ce18fc0b459e34cf1a5e212f4e5fbe2eb136b3c18092ae9905 ;; \
    esac \
 && cd /tmp \
 && curl -fsSLo nvim.tar.gz "https://github.com/neovim/neovim/releases/download/v0.11.7/${nvim}.tar.gz" \
 && echo "${sum}  nvim.tar.gz" | sha256sum -c - \
 && tar -xzf nvim.tar.gz -C /opt \
 && ln -s "/opt/${nvim}/bin/nvim" /usr/local/bin/nvim \
 && rm nvim.tar.gz
RUN case "$(dpkg --print-architecture)" in \
      amd64) wasi=x86_64; sum=b761e3a0721dbae9c09a0059e5fdb2bf917d1b4a8a7b430fb3b5aafb0984b2c4 ;; \
      arm64) wasi=arm64; sum=f7e243dff54d60bcc576e94d6166b69f410f2500ae4a9ceef34315be10e77971 ;; \
    esac \
 && cd /tmp \
 && curl -fsSLo wasi-sdk.tar.gz "https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-34/wasi-sdk-34.0-${wasi}-linux.tar.gz" \
 && echo "${sum}  wasi-sdk.tar.gz" | sha256sum -c - \
 && tar -xzf wasi-sdk.tar.gz -C /opt \
 && ln -s "/opt/wasi-sdk-34.0-${wasi}-linux" /opt/wasi-sdk \
 && rm wasi-sdk.tar.gz
ENV ORIEL_NVIM=/usr/local/bin/nvim
ENV WASI_SDK_PATH=/opt/wasi-sdk
