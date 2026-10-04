# Build from the repo root: docker build . -f agent/server/build/Dockerfile
# The Go tag matches agent/server/go.mod, so the image is built with the Go
# version CI tested.
FROM golang:1.27.1-alpine AS go-build
LABEL org.opencontainers.image.source=https://github.com/jyothri/bhandaar
WORKDIR /src/agent/server
COPY agent/server/go.mod agent/server/go.sum ./
COPY agent/wire/go.mod ../wire/
RUN go mod download
COPY agent/wire/ ../wire/
COPY agent/server/ .
# The commit it's built from, shown as server_version "0.1.0+<sha>" (CI
# passes it; a local build without it says "unknown").
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/jyothri/bhandaar/agent/server/internal/api.Commit=${COMMIT}" \
      -o /out/agentserver ./cmd/agentserver

FROM alpine
RUN apk --no-cache add ca-certificates && adduser -D -H agentserver
COPY --from=go-build /out/agentserver /usr/local/bin/agentserver
USER agentserver
EXPOSE 8091
ENTRYPOINT ["agentserver"]
CMD ["serve"]
