package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"wangjui.com/enproxy/proxy"
	"wangjui.com/enproxy/proxy/common"
)

func main() {
	// 保留项目现有日志初始化方式。
	common.InitLogger("enproxy", "debug")

	configPath := "config.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// 只传配置文件路径：账号、路由、DNS、流量统计和全部入站代理
	// 都在 ProxyManager 内部创建，不再由 main 逐项组装。
	proxyManager, err := proxy.NewProxyManager(configPath)
	if err != nil {
		common.Log.Fatalf("initialize proxy manager failed: %v", err)
	}
	defer proxyManager.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proxyManager.Start()
	common.Log.Infof("enproxy startup initiated: %d inbounds", proxyManager.Count())

	<-ctx.Done()
	common.Log.Info("shutdown signal received, stopping enproxy")
}
