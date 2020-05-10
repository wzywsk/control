package main

import (
	"easy/box/boxconfig"
	"fmt"
)

type sshClient struct {
}

func newSSHClient(cfg *boxconfig.BoxConfig) *sshClient {
	s := new(sshClient)
	return s
}
func (s *sshClient) Start(addr, user, password string) (err error) {
	err = fmt.Errorf("windows 暂不支持此功能")
	return
}
