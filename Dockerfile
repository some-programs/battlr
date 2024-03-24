from golang:1.22 as builder
copy go.mod go.sum ./
run go mod download
workdir /app
copy . /app/
run CGO_ENABLED=0 GOOS=linux go build -o /battlr

from gcr.io/distroless/base
copy --from=builder /battlr /battlr
entrypoint ["/battlr"]
expose 80
volume /battles
volume /data
env BATTLR_DIR=/battles/
env BATTLR_DB=/data/battlr.db
env BATTLR_LISTEN=80
