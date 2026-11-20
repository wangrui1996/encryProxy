package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"wangjui.com/spider/proxy"
	"wangjui.com/spider/proxy/common"
)

func main() {
	// 保留项目现有日志初始化方式。
	common.InitLogger("spider", "debug")
	common.Log.Infof("spider version: %s", proxy.VERSION)
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

	if err := proxyManager.Start(); err != nil {
		common.Log.Fatalf("start proxy manager failed: %v", err)
	}
	common.Log.Infof("spider startup initiated: %d inbounds", proxyManager.Count())

	<-ctx.Done()
	common.Log.Info("shutdown signal received, stopping spider")
}
