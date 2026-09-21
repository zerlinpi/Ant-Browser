FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY server/go.mod server/go.sum ./server/
RUN cd server && go mod download
COPY server ./server
WORKDIR /src/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/control-plane ./cmd/control-plane
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/control-plane /app/control-plane
COPY --from=build /out/migrate /app/migrate
COPY database/migrations /app/database/migrations
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/control-plane"]
