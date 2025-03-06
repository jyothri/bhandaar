FROM golang:alpine AS go-build

LABEL org.opencontainers.image.source=https://github.com/jyothri/bhandaar

WORKDIR /usr/src/app
COPY be .
RUN apk --update add ca-certificates
RUN CGO_ENABLED=0 go build

FROM alpine

WORKDIR /usr/app
COPY --from=go-build /usr/src/app/hdd ./
COPY --from=go-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8090
CMD ["sh", "-c", "./hdd \
  -oauth_client_id=$OAUTH_CLIENT_ID \
  -oauth_client_secret=$OAUTH_CLIENT_SECRET \
  -frontend_url=$FRONTEND_URL"]