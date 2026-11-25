# Build with podman: podman build -t localhost/travel-next:dev .
# (Also valid as a Containerfile — podman reads this file unchanged.)

FROM docker.io/library/golang:1.26-alpine AS build
WORKDIR /src

# Dependencies first, so edits to the source do not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO off and a trimmed path give a static binary that runs on a scratch-like base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM docker.io/library/alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 app
COPY --from=build /out/server /usr/local/bin/server
# The default prompt context ships in the image; Kubernetes mounts a ConfigMap
# over PROMPT_CONTEXT_FILE to override it without a rebuild.
COPY deploy/k8s/prompt-context.yaml /etc/travel-next/prompt-context.yaml
USER 10001:10001
ENV ADDR=:8080 \
    PROMPT_CONTEXT_FILE=/etc/travel-next/prompt-context.yaml
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
