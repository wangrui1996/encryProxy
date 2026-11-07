package main

import (
	"encoding/json"
	"wangjui.com/enproxy/proxy"
	"os"
	"path/filepath"
	com "wangjui.com/enproxy/common"
)

var cfg proxy.Config

func readConfig(cfg *proxy.Config)  {
	com.InitLogger("enproxy", "debug")
	configFileName := "config.json"
	if len(os.Args) > 1 {
		configFileName = os.Args[1]
	}
	configFileName, _ = filepath.Abs(configFileName)
	com.Log.Infof("Loading config: %v", configFileName)

	configFile, err := os.Open(configFileName)
	if err != nil {
		com.Log.Fatalf("File error: ", err.Error())
	}
	defer configFile.Close()

	jsonParser := json.NewDecoder(configFile)

	if err := jsonParser.Decode(&cfg); err != nil {
		com.Log.Fatalf("Config error: ", err.Error())
	}
}

func main() {
	readConfig(&cfg)

	// 创建流量统计管理器
	trafficManager := proxy.NewTrafficManager()
	go trafficManager.Start()

	// 创建代理实例，共享流量统计管理器
	p := proxy.NewProxy(&cfg, trafficManager)
	go p.Run()
	
	webServer := proxy.NewWebServer(cfg.Web.Listen, trafficManager)
	go webServer.Run()

	select {} // 阻塞主线程，防止退出
}

