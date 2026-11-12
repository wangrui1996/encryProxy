
GOOS=linux GOARCH=amd64 go build -o bin/enproxy-linux-x64 ./cmd

GOOS=linux GOARCH=arm64 go build -o bin/enproxy-linux-arm64 ./cmd