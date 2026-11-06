BINARY := netproxy
CMD := ./cmd/netproxy

.PHONY: build test clean

# 编译程序
build:
	go build -o bin/$(BINARY) $(CMD)

# 执行测试
test:
	go test ./...

# 格式化代码
fmt:
	gofmt -w .

# 整理依赖
tidy:
	go mod tidy

# 清理构建产物
clean:
	rm -rf bin/