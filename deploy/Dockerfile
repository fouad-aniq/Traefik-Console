FROM --platform=$BUILDPLATFORM oven/bun:1.4.4@sha256:1d22548f177f4ff38e0c15baa3c2c46c6c2060ba2d1bc573bb4720b407548f1d AS bun
FROM --platform=$BUILDPLATFORM node:24.11.0-bookworm-slim@sha256:76d0ed0ed93bed4f4376211e9d8fddac4d8b3fbdb54cc45955696001a3c91152 AS frontend
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
WORKDIR /src
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/index.html frontend/tsconfig.json frontend/svelte.config.js frontend/vite.config.ts ./
COPY frontend/src ./src
# Favicons, app icons and the web manifest: copied as-is into dist/assets by Vite.
COPY frontend/public ./public
ARG VERSION=dev
RUN APP_VERSION="$VERSION" bun run build

FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm@sha256:dc0963f32053e4bc451d24d87e3892d42d01021b61f093e98eb95c0352bb84b0 AS backend
WORKDIR /src
ENV GOTOOLCHAIN=local CGO_ENABLED=0
COPY backend/go.mod backend/go.sum ./
RUN go mod download && go mod verify
COPY backend/cmd ./cmd
COPY backend/internal ./internal
COPY --from=frontend /src/dist ./cmd/server/dist
ARG TARGETARCH
ARG VERSION=dev
RUN GOOS=linux GOARCH=$TARGETARCH go build -mod=readonly -trimpath -buildvcs=false -tags embedded_ui -ldflags="-s -w -X github.com/traefik-console/internal/version.Version=${VERSION}" -o /out/server ./cmd/server
RUN mkdir -p -m 0700 /seed/state/keys /seed/state/data && chmod 0700 /seed/state

FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
COPY --from=backend /out/server /usr/local/bin/server
COPY --from=backend --chown=1000:1000 /seed/state /var/lib/console
COPY backend/THIRD-PARTY-NOTICES.md /usr/share/licenses/console/backend.md
COPY frontend/THIRD-PARTY-NOTICES.md /usr/share/licenses/console/frontend.md
ENV BIND_ADDRESS=0.0.0.0 PORT=8081 DB_PATH=/var/lib/console/data/console.db MASTER_KEY_FILE=/var/lib/console/keys/master.key STATIC_FILE=/config/static/traefik.yml DYNAMIC_DIR=/config/dynamic SECRETS_DIR=/config/secrets
USER 1000:1000
EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=3s CMD ["server","healthcheck"]
ENTRYPOINT ["/usr/local/bin/server"]
