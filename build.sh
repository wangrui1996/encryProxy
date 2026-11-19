
GOOS=linux GOARCH=amd64 go build -o bin/enproxy-linux-x64 ./cmd

GOOS=linux GOARCH=arm64 go build -o bin/enproxy-linux-arm64 ./cmd

# Windows 64 位 (x86_64)
GOOS=windows GOARCH=amd64 go build -o bin/enproxy-windows-x64.exe ./cmd

# Windows ARM64
GOOS=windows GOARCH=arm64 go build -o bin/enproxy-windows-arm64.exe ./cmd
