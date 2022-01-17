FROM node:16 as node-build

WORKDIR /usr/src/app
COPY ./web/svelte/ ./
RUN npm install
RUN npm run build

FROM golang:alpine as go-build

WORKDIR /usr/src/app
COPY . .
RUN rm -rf web/svelte
RUN CGO_ENABLED=0 go build

FROM scratch

WORKDIR /usr/app
COPY --from=go-build /usr/src/app/hdd ./
COPY --from=node-build /usr/src/app/public/ ./web/svelte/public/
EXPOSE 8090
CMD ["./hdd", "-start_web_server=true"]