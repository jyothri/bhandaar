# build environment
FROM node:22-alpine AS builder
LABEL org.opencontainers.image.source=https://github.com/jyothri/bhandaar
WORKDIR /usr/src/app
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

# production environment
FROM nginx:stable-alpine
COPY --from=builder /usr/src/app/dist /usr/share/nginx/html
EXPOSE 80
