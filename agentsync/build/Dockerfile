# Build from the repo root: docker build . -f agentsync/build/Dockerfile
# The Go tag matches agentsync/go.mod, so the image is built with the Go
# version CI tested.
FROM golang:1.27.1-alpine AS go-build
LABEL org.opencontainers.image.source=https://github.com/jyothri/bhandaar
WORKDIR /src/agentsync
COPY agentsync/go.mod agentsync/go.sum ./
COPY agentsync/wire/go.mod wire/
RUN go mod download
COPY agentsync/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/agentsync ./cmd/agentsync

FROM alpine
RUN apk --no-cache add ca-certificates && adduser -D -H agentsync
COPY --from=go-build /out/agentsync /usr/local/bin/agentsync
USER agentsync
EXPOSE 8091
ENTRYPOINT ["agentsync"]
CMD ["serve"]
