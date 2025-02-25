# build environment
FROM node:latest AS builder
RUN mkdir /usr/src/app
WORKDIR /usr/src/app
ENV PATH=/usr/src/app/node_modules/.bin:$PATH
COPY package.json /usr/src/app/
COPY package-lock.json /usr/src/app/
RUN npm install --silent
COPY . /usr/src/app
RUN npm run build

# production environment
FROM nginx:alpine
COPY --from=builder /usr/src/app/dist /usr/share/nginx/html
EXPOSE 80