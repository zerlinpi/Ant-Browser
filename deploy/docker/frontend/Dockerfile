FROM node:20-bookworm-slim AS build

WORKDIR /src/desktop/vue3-ui
COPY desktop/vue3-ui/package.json desktop/vue3-ui/package-lock.json ./
RUN npm ci
COPY desktop/vue3-ui ./
# AppShell intentionally reuses the established product logo while the rest of
# the commercial client is isolated under desktop/vue3-ui.
COPY frontend/src/resources/images/logo.png /src/frontend/src/resources/images/logo.png
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine AS runtime
COPY deploy/nginx/frontend.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/desktop/wails-client/frontend/dist /usr/share/nginx/html
USER 101:101
EXPOSE 8080
