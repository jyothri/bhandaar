FROM node:16 as build

WORKDIR /usr/src/app
COPY ./web/svelte/ ./
RUN npm install
RUN npm run build

FROM node:16-alpine

COPY --from=build /usr/src/app /
EXPOSE 8080
CMD ["npm", "start"]