FROM node:16 as node-build

WORKDIR /usr/src/app
COPY ./web/svelte/ ./
RUN npm install
RUN npm run build

FROM golang:alpine as go-build

WORKDIR /usr/src/app
COPY . .
RUN rm -rf web/svelte
COPY --from=node-build /usr/src/app/public/ /usr/src/app/web/svelte/public/
RUN CGO_ENABLED=0 go build

FROM scratch

WORKDIR /usr/app
COPY --from=go-build /usr/src/app/hdd ./
COPY --from=go-build /usr/src/app/web ./web
EXPOSE 8090
CMD ["./hdd"]