package main

import (
	"encoding/json"
	"portForward/proxy"
	"log"
	"os"
	"path/filepath"
)

var cfg proxy.Config

func readConfig(cfg *proxy.Config)  {
	configFileName := "config.json"
	if len(os.Args) > 1 {
		configFileName = os.Args[1]
	}
	configFileName, _ = filepath.Abs(configFileName)
	log.Printf("Loading config: %v", configFileName)

	configFile, err := os.Open(configFileName)
	if err != nil {
		log.Fatal("File error: ", err.Error())
	}
	//byteValue, _ := ioutil.ReadAll(configFile)
	//fmt.Println(string(byteValue))
	defer configFile.Close()


	//if err := json.Unmarshal(byteValue, &cfg); err != nil {
	//		log.Fatal("Config error: ", err.Error())
	//	}
	jsonParser := json.NewDecoder(configFile)
	//fmt.Println(jsonParser.Buffered())
	//os.Exit(0)
	if err := jsonParser.Decode(&cfg); err != nil {
		log.Fatal("Config error: ", err.Error())
	}
}

func main() {
	readConfig(&cfg)
	//fmt.Printf("%v\n", cfg)
	proxy := proxy.NewProxy(&cfg)
	proxy.Run()
}

