package main

import (
	"easy/box/boxconfig"
	"log"
)

func main() {
	//读取配置文件
	cfg, err := boxconfig.LoadEndConfig("box.conf")
	if err != nil {
		log.Println("加载配置文件出错", err)
		return
	}
	b := NewBoxControl(cfg)
	log.Fatalln(b.Start())
}
