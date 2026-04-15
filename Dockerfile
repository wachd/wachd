# Copyright 2025 NTC Dev
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# ── Stage 1: Go binaries ──────────────────────────────────────────────────────
FROM golang:1.25-alpine AS go-builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Cache dependencies separately from source
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build both binaries — CGO disabled for fully static executables
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /out/worker ./cmd/worker

# ── Stage 2: Next.js standalone build ────────────────────────────────────────
FROM node:22-alpine AS web-builder

WORKDIR /web

# Cache dependencies separately from source
COPY web/package*.json ./
RUN npm ci

COPY web/ .

# output: 'standalone' in next.config.ts produces a self-contained server.js
RUN npm run build

# ── Stage 3: Runtime ─────────────────────────────────────────────────────────
# node:22-alpine hosts both the statically-compiled Go binaries and the
# Next.js standalone server. Three roles, one image — the Helm values.yaml
# selects the role via command:
#   server  → CMD ["/server"]            (default)
#   worker  → command: ["/worker"]
#   web     → command: ["node", "server.js"]  workingDir: /web
FROM node:22-alpine

# Timezone data and CA certificates from the Go builder
COPY --from=go-builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=go-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Go binaries
COPY --from=go-builder /out/server /server
COPY --from=go-builder /out/worker /worker

# Next.js standalone output
COPY --from=web-builder /web/.next/standalone /web
COPY --from=web-builder /web/.next/static     /web/.next/static
COPY --from=web-builder /web/public           /web/public

WORKDIR /web

EXPOSE 8080 3000
USER node
CMD ["/server"]
