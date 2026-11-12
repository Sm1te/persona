# The hub as a container image (docs/operations/docker.md): personad hub with the web client inside, which is also the node build
# the app's "Redeploy" sends computers of the image's platform (the others' are built in, or come from the release when
# the image is built with UPDATE_URL, docs/operations/docker.md "Node builds"). Built for linux/amd64 and linux/arm64
# (.github/workflows/release.yml); the binaries are cross compiled on the build machine, only the last stage runs on
# the target platform.
#
#   docker build -t persona-hub .
#
# No skills are inside: the assistant's come from ~/.persona/skills on its machine (docs/operations/hub-update.md "Skills out of the binary").

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS go-toolchain

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
# The browser's OPAQUE implementation is built from the same Go sources as the hub.
COPY --from=go-toolchain /usr/local/go/ /usr/local/go/
ENV PATH="/usr/local/go/bin:${PATH}"
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
# Packages the project keeps in web/vendor/ (SheetJS publishes its current releases only as a tarball, docs/web/web.md "Spreadsheets").
COPY web/vendor/ ./vendor/
# A lockfile written against a mirror names the mirror's host, which no other network can reach: refuse it rather
# than fail later in npm (docs/lessons/server.md).
RUN if grep -E '"resolved": "' package-lock.json | grep -v '"file:vendor/' | grep -vq '"https://registry.npmjs.org/'; then \
      echo "web/package-lock.json resolves packages from somewhere other than registry.npmjs.org or web/vendor" >&2; exit 1; \
    fi \
 && npm ci --no-audit --no-fund
COPY web/ ./
COPY server/go.mod server/go.sum /src/server/
COPY sdk/go/ /src/sdk/go/
COPY server/internal/passwordpake/ /src/server/internal/passwordpake/
COPY server/cmd/password-wasm/ /src/server/cmd/password-wasm/
# The shared cases the web app's tests import (src/lib/*.test.ts read ../../../spec/fixtures/<case>/*.json): tsc -b in
# the build checks the tests too. The whole folder (about 2 MB, this stage only), so a test reading a new case folder
# needs no line here; four image builds broke on a missing line before (#5197).
COPY spec/fixtures/ /src/spec/fixtures/
# The extension schema, which the web app builds in: src/lib/extension-ui-stream.ts imports its *.schema.json (?raw),
# the admin page's assistant card preview and the showcase glob its examples (import.meta.glob, which comes out empty
# without them), and tests import example JSON (TS2307 in tsc -b without it).
COPY schema/extension/ /src/schema/extension/
# Without prebuild's privacy check: it scans the whole checkout (scripts/, git), which is not in this context. It
# runs on the checkout before the image is built (.github/workflows/release.yml; locally the hooks and web builds).
RUN npm run pake:build && npm run build --ignore-scripts

FROM go-toolchain AS build
# VERSION: what the hub reports and compares with its nodes' (a node of another build gets "Redeploy"). The image's
# hub never replaces itself (channel docker): it says when a newer image is out (docs/operations/hub-update.md), looking up the
# tags of IMAGE (the workflow passes ghcr.io/<owner>/<repo>) unless the compose file says which image it runs.
ARG VERSION=dev
ARG IMAGE=
# UPDATE_URL: a release's dl/ (the publisher's site; empty: none, node_packages in hub.json can still say). The
# manager fetches the node builds for the computers' platforms from there, of this version or the newest it drives
# (docs/operations/node-update.md "Node packages"); the image never updates itself from it (channel docker).
ARG UPDATE_URL=
WORKDIR /src/server
# The extension SDK's wire types (sdk/go): server/go.mod replaces the module with ../sdk/go.
COPY sdk/go/ /src/sdk/go/
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist/ internal/hub/webdist/
# Its hashed assets kept gzipped, as the hub sends them (docs/web/web.md "Precompressed assets").
RUN go run ./internal/webasset/precompress internal/hub/webdist
# The runtime packages personad carries and keeps its runtimes on (docs/integrations/runtimes.md "Auto-update"): the DSH plugin's and
# the pi extension's, the Paseo bridge's and the Agent SDK bridge's code.
COPY integrations/agent-sdk/ /src/integrations/agent-sdk/
COPY integrations/dsh/ /src/integrations/dsh/
COPY integrations/paseo/ /src/integrations/paseo/
COPY integrations/pi/ /src/integrations/pi/
COPY integrations/runtime-sdk/ /src/integrations/runtime-sdk/
RUN go generate ./internal/runtimepkg
# The node builds. With UPDATE_URL (a release's dl/ to fetch from), the image's own build only, for its platform: the
# hub runs it, and it is the node build of that platform too; other platforms' builds and persona-node (the device's
# own program) come from the release (node_packages in hub.json can point elsewhere), or an air-gapped hub's owner puts
# them in the data volume's bin/nodes (docs/operations/docker.md "Node builds"). Without one, nothing could be fetched: every
# platform in NODE_PLATFORMS is built in, as before (it must include linux/<the image's architecture>, which the hub
# itself runs; the PR smoke build passes linux/amd64 only).
ARG TARGETARCH
ARG NODE_PLATFORMS="linux/amd64 linux/arm64 darwin/arm64 darwin/amd64"
# Every program is built with server/build-tags.txt, as the release is: it leaves out the tailscale.com features
# persona never uses (server/internal/buildtags).
RUN if [ -n "$UPDATE_URL" ]; then plats="linux/$TARGETARCH"; else plats="$NODE_PLATFORMS"; fi; \
    for t in $plats; do \
      CGO_ENABLED=0 GOOS=${t%/*} GOARCH=${t#*/} go build -tags "$(cat build-tags.txt)" -trimpath \
        -ldflags "-s -w -X main.version=$VERSION -X main.channel=docker -X main.image=$IMAGE -X main.updateURL=$UPDATE_URL" \
        -o /out/nodes/personad-${t%/*}-${t#*/} ./cmd/personad || exit 1; \
    done
# The hub's and the manager's own programs (docs/server/server.md "Programs"), the same build as personad.
RUN for p in persona-hub persona-manager; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -tags "$(cat build-tags.txt)" -trimpath \
        -ldflags "-s -w -X main.version=$VERSION -X main.channel=docker -X main.image=$IMAGE -X main.updateURL=$UPDATE_URL" -o /out/$p ./cmd/$p || exit 1; \
    done

FROM alpine:3.22
ARG TARGETARCH
# ssh: machines the hub reaches over ssh (optional with tailcat); su-exec: the entrypoint drops to the persona user.
RUN apk add --no-cache ca-certificates tzdata openssh-client su-exec \
 && adduser -D -h /home/persona -u 1000 persona \
 && mkdir -p /home/persona/.persona && chown persona:persona /home/persona/.persona && chmod 700 /home/persona/.persona
COPY --from=build /out/nodes/ /opt/persona/nodes/
RUN ln -s /opt/persona/nodes/personad-linux-$TARGETARCH /opt/persona/personad && /opt/persona/personad version
# Next to the binary personad runs as (the link resolves into nodes/), where the hub looks for it.
COPY --from=build /out/persona-hub /out/persona-manager /opt/persona/nodes/
RUN for p in persona-hub persona-manager; do [ "$(/opt/persona/nodes/$p version -v)" = "$(/opt/persona/personad version -v)" ] || exit 1; done
COPY deploy/docker/personad.sh /usr/local/bin/personad
COPY deploy/docker/entrypoint.sh /usr/local/bin/persona-entrypoint
# Command and artifact aliases share the exact binaries; never create another state store.
RUN ln -s personad /usr/local/bin/jarvisd \
 && ln -s persona-entrypoint /usr/local/bin/jarvis-entrypoint \
 && ln -s /opt/persona /opt/jarvis \
 && ln -s personad /opt/persona/jarvisd \
 && for binary in /opt/persona/nodes/personad-*; do \
      name="${binary##*/}"; ln -s "$name" "/opt/persona/nodes/jarvisd-${name#personad-}"; \
    done \
 && mkdir -p /home/jarvis \
 && chown persona:persona /home/jarvis \
 && ln -s /home/jarvis/.ssh /home/persona/.ssh
# ~/.persona is the data volume (hub.json, keys, the knowledge base, ~/.persona/bin/nodes where node builds of other
# platforms can be put, ~/.persona/cache/nodes where the fetched ones are kept); the image's own build is bundled outside
# it. No local node: this machine has no herdr or claude for the hub to drive.
ENV HOME=/home/persona \
    PERSONA_LOCAL_NODE=0 \
    PERSONA_NODE_BUNDLED=/opt/persona/nodes
# 18660 the hub (HTTP), 18662 its TLS port (public.tls_listen: on for a first start, deploy/docker/entrypoint.sh).
EXPOSE 18660 18662
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s \
  CMD wget -qO- http://127.0.0.1:18660/api/v1/health >/dev/null || exit 1
ENTRYPOINT ["persona-entrypoint"]
# The entrypoint runs `hub run` under personad supervise: the built-in manager beside the hub when hub.json says
# assistant.mode service (new installs), each started again on its own; otherwise the hub alone, as before.
CMD ["hub", "run"]
