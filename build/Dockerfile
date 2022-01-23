FROM node:16 as node-build

WORKDIR /usr/src/app
COPY ./web/svelte/ ./
RUN npm install
RUN npm run build

FROM golang:alpine as go-build

WORKDIR /usr/src/app
COPY . .
RUN rm -rf web/svelte
RUN apk --update add ca-certificates
RUN CGO_ENABLED=0 go build

FROM alpine

WORKDIR /usr/app
COPY --from=go-build /usr/src/app/hdd ./
COPY --from=node-build /usr/src/app/public/ ./web/svelte/public/
COPY --from=go-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8090
CMD ["sh", "-c", "./hdd \
  -start_web_server=true \
  -oauth_client_id=$OAUTH_CLIENT_ID \
  -oauth_client_secret=$OAUTH_CLIENT_SECRET \
  -refresh_token=$REFRESH_TOKEN"]