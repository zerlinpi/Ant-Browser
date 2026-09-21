FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY server/go.mod server/go.sum ./server/
RUN cd server && go mod download
COPY server ./server
WORKDIR /src/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/worker ./cmd/worker

FROM golang:1.25-bookworm AS probe-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY backend ./backend
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/proxy-probe ./backend/cmd/proxy-probe

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=probe-build /out/proxy-probe /app/proxy-probe
COPY --from=build /out/worker /app/worker
COPY database/migrations /app/database/migrations
USER nonroot:nonroot
ENTRYPOINT ["/app/worker"]
