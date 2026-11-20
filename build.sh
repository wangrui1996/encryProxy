
GOOS=linux GOARCH=amd64 go build -o bin/spider-linux-x64 ./cmd

GOOS=linux GOARCH=arm64 go build -o bin/spider-linux-arm64 ./cmd

# Windows 64 位 (x86_64)
GOOS=windows GOARCH=amd64 go build -o bin/spider-windows-x64.exe ./cmd

# Windows ARM64
GOOS=windows GOARCH=arm64 go build -o bin/spider-windows-arm64.exe ./cmd
