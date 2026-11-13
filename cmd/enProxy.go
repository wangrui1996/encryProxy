package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"wangjui.com/enproxy/proxy"
	"wangjui.com/enproxy/proxy/common"

	"wangjui.com/enproxy/proxy/config"
)



func readConfig(cfg *config.Config) error {
	configFileName := "config.json"

	if len(os.Args) > 1 {
		configFileName = os.Args[1]
	}

	absPath, err := filepath.Abs(configFileName)
	if err != nil {
		return fmt.Errorf("resolve config path failed: %w", err)
	}

	common.Log.Infof("Loading config: %s", absPath)

	configFile, err := os.Open(absPath)
	if err != nil {
		return fmt.Errorf("open config file failed: %w", err)
	}
	defer configFile.Close()

	jsonParser := json.NewDecoder(configFile)

	if err := jsonParser.Decode(cfg); err != nil {
		return fmt.Errorf("decode config failed: %w", err)
	}

	return nil
}

func main() {
	// 初始化日志。
	common.InitLogger("enproxy", "debug")

	// 加载配置。
	var cfg config.Config
	if err := readConfig(&cfg); err != nil {
		common.Log.Fatalf("load config failed: %v", err)
	}

	// 创建流量统计管理器。
	trafficManager := proxy.NewTrafficManager()

	// 创建代理管理器。
	proxyManager, err := proxy.NewProxyManager(cfg, trafficManager)
	if err != nil {
		common.Log.Fatalf("create proxy manager failed: %v", err)
	}

	// 启动流量统计。
	go trafficManager.Start()

	// 启动所有代理入口，以及配置中启用的 Web Dashboard。
	proxyManager.Start()

	common.Log.Infof("started %d proxy instances", proxyManager.Count())

	// 阻塞主线程，防止程序退出。
	select {}
}