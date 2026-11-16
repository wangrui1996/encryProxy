package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"wangjui.com/enproxy/proxy"
	"wangjui.com/enproxy/proxy/common"
	"wangjui.com/enproxy/proxy/config"
	"wangjui.com/enproxy/proxy/stat"
)

func readConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	configFileName := "config.json"
	if len(os.Args) > 1 {
		configFileName = os.Args[1]
	}

	absPath, err := filepath.Abs(configFileName)
	if err != nil {
		return fmt.Errorf("resolve config path failed: %w", err)
	}

	common.Log.Infof("Loading config: %s", absPath)

	loadedConfig, err := config.Load(absPath)
	if err != nil {
		return err
	}

	*cfg = *loadedConfig
	return nil
}

func main() {
	// 保留现有日志初始化方式。
	common.InitLogger("enproxy", "debug")

	// 加载配置文件。
	var cfg config.Config
	if err := readConfig(&cfg); err != nil {
		common.Log.Fatalf("load config failed: %v", err)
	}

	// Outbounds 只包含自定义出站。
	// freedom、blackhole 由路由管理器自动注册，无需在此添加。
	common.Log.Infof(
		"config loaded: %d inbounds, %d custom outbounds, %d accounts, %d routes; builtin outbounds: freedom, blackhole",
		len(cfg.Inbounds),
		len(cfg.Outbounds),
		len(cfg.Accounts),
		len(cfg.Route),
	)

	// 创建流量统计管理器。
	trafficManager := stat.NewManager()

	// 创建代理管理器。
	proxyManager, err := proxy.NewProxyManager(
		cfg,
		trafficManager,
	)
	if err != nil {
		trafficManager.Close()
		common.Log.Fatalf(
			"initialize proxy manager failed: %v",
			err,
		)
	}
	defer proxyManager.Close()

	// 监听退出信号。
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	// 启动所有代理服务。
	proxyManager.Start()

	common.Log.Infof(
		"enproxy startup initiated: %d inbounds",
		proxyManager.Count(),
	)

	// 阻塞主线程，等待退出信号。
	<-ctx.Done()

	common.Log.Info(
		"shutdown signal received, stopping enproxy",
	)
}
