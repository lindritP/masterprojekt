# Demo UI (ADR 0014): static build served by nginx. Runtime settings come from
# environment variables and are written to /config.js when the container starts.
FROM node:22.21-alpine AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.29-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
COPY docker-entrypoint.d/40-ccsra-config.sh /docker-entrypoint.d/40-ccsra-config.sh
# The entrypoint runs unprivileged and rewrites config.js.
USER root
RUN chown 101:101 /usr/share/nginx/html/config.js
USER 101
