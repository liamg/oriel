package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// BaseDockerfile is the default sandbox image: build tools, terraform, mise,
// which installs each project's toolchains (see runner.Provision), and a
// headless Chromium for screenshots and browser tests. oriel and the agent are
// not in it: they're mounted from the host, or installed with mise, at launch.
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

// PublishedRepo is where CI publishes the base image, tagged as BaseImage is.
const PublishedRepo = "ghcr.io/liamg/oriel-sandbox"

// baseDigest is the published base image's digest, set when CI links a release:
// -ldflags "-X github.com/liamg/oriel/sandbox/container.baseDigest=sha256:…".
var baseDigest string

// PublishedBase is where the base image is pulled from: by digest in a release, else by its tag.
func PublishedBase() string {
	if baseDigest != "" {
		return PublishedRepo + "@" + baseDigest
	}
	_, sum, _ := strings.Cut(BaseImage(), ":")
	return PublishedRepo + ":" + sum
}

var buildMu sync.Mutex

// EnsureImage makes tag from dockerfile unless it is already present, its output going to the file log. The base
// image is pulled from PublishedBase first, and built only when that fails.
func EnsureImage(ctx context.Context, tag, dockerfile, log string) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	if ImageExists(ctx, tag) {
		return nil
	}
	if dockerfile == BaseDockerfile && tag == BaseImage() {
		f, err := openLog(log)
		if err != nil {
			return err
		}
		defer f.Close()
		err = pull(ctx, PublishedBase(), tag, f)
		if err == nil {
			return nil
		}
		fmt.Fprintf(f, "\n%v\nBuilding it here instead.\n\n", err)
		return Build(ctx, tag, dockerfile, f, false)
	}
	return buildLogged(ctx, tag, dockerfile, log, false)
}

// pull pulls ref and tags it as tag, writing its progress to out.
func pull(ctx context.Context, ref, tag string, out io.Writer) error {
	e := EngineOf(ctx)
	cmd := e.cmd(ctx, "pull", ref)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s pull %s: %w", e.Bin, ref, err)
	}
	_, err := Run(ctx, "tag", ref, tag)
	return err
}

// Rebuild builds tag from dockerfile again, skipping the cache so its downloads are fetched afresh.
func Rebuild(ctx context.Context, tag, dockerfile, log string) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	return buildLogged(ctx, tag, dockerfile, log, true)
}

func buildLogged(ctx context.Context, tag, dockerfile, log string, noCache bool) error {
	f, err := openLog(log)
	if err != nil {
		return err
	}
	defer f.Close()
	return Build(ctx, tag, dockerfile, f, noCache)
}

func openLog(log string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		return nil, err
	}
	return os.Create(log)
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
