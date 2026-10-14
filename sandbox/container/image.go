package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// BaseDockerfile is the default sandbox image: build tools, terraform, mise,
// which installs each project's toolchains (see runner.Provision), and a
// headless Chromium for screenshots and browser tests. claude and oriel are not
// in it: both are mounted from the host at launch.
const BaseDockerfile = `FROM debian:bookworm-slim
ARG MISE_VERSION=v2026.6.10
ARG TERRAFORM_VERSION=1.16.4
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git ca-certificates curl wget ripgrep openssh-client less procps jq make gcc g++ libc6-dev \
      python3 unzip xz-utils zip tar file tree \
      chromium-headless-shell fonts-liberation \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL "https://github.com/jdx/mise/releases/download/${MISE_VERSION}/install.sh" | MISE_INSTALL_PATH=/usr/local/bin/mise sh \
 && arch="$(dpkg --print-architecture)" \
 && zip="terraform_${TERRAFORM_VERSION}_linux_${arch}.zip" \
 && cd /tmp \
 && curl -fsSLO "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/${zip}" \
 && curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_SHA256SUMS" | grep " ${zip}$" | sha256sum -c - \
 && unzip -q "${zip}" terraform -d /usr/local/bin \
 && rm "${zip}"
`

// BaseImage is the tag the base image is built as: it changes with the
// Dockerfile, so an update rebuilds it.
func BaseImage() string {
	sum := sha256.Sum256([]byte(BaseDockerfile))
	return "oriel-sandbox:" + hex.EncodeToString(sum[:])[:12]
}

var buildMu sync.Mutex

// EnsureImage builds tag from dockerfile unless it is already present.
func EnsureImage(ctx context.Context, tag, dockerfile string) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	if ImageExists(ctx, tag) {
		return nil
	}
	return Build(ctx, tag, dockerfile)
}

// ProjectImage is the tag for a project's own approved Dockerfile.
func ProjectImage(dockerfile string) string {
	sum := sha256.Sum256([]byte(dockerfile))
	return "oriel-project:" + hex.EncodeToString(sum[:])[:12]
}

// Sum is the hash a Dockerfile is approved by.
func Sum(dockerfile string) string {
	sum := sha256.Sum256([]byte(dockerfile))
	return hex.EncodeToString(sum[:])
}
