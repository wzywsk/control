// +build linux darwin

package main

import (
	"os"
	"os/exec"

	log "github.com/sirupsen/logrus"
)

func update() {
	file, err := os.Create("boxupdate.log")
	if err != nil {
		return
	}
	log.SetFormatter(&log.TextFormatter{})
	log.SetOutput(file)
	defer func() {
		os.Exit(0)
		file.Close()
	}()
	//先关闭以前的box
	log.Info("开始停止运行box")
	cmd := exec.Command("/usr/local/bin/supervisorctl", "stop", "box")
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		log.WithField("msg", "停止box失败").Errorln(err)
		return
	}
	//替换并备份原来的文件
	log.Info("开始替换并备份文件")
	if err := replaceFile("box", "box-new"); err != nil {
		log.WithField("msg", "替换文件失败").Errorln(err)
		return
	}
	//启动box
	log.Info("开始启动box")
	cmd = exec.Command("/usr/local/bin/supervisorctl", "start", "box")
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		log.WithField("msg", "启动box失败").Errorln(err)
		return
	}
}
