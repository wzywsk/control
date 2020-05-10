// +build linux darwin

package main

import (
	"net"
	"time"

	"fmt"

	"os"
	"os/exec"

	"io"

	"sync/atomic"

	"easy/box/boxconfig"

	"github.com/kr/pty"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/terminal"
)

//SSHClient ssh客户端,连接成功后将接收终端请求.
//如果再次收到连接请求,将关闭以前的.
type sshClient struct {
	conn       ssh.Conn
	status     int32
	cfg        *boxconfig.BoxConfig
	p          *os.File
	contextLog *logrus.Entry
}

func newSSHClient(cfg *boxconfig.BoxConfig) *sshClient {
	s := new(sshClient)
	s.cfg = cfg
	s.contextLog = logrus.WithField("module", "ssh")
	return s
}

//Start 开始连接服务端,出错将不会重连直接返回错误.
//用户名密码是服务端传过来临时的
func (s *sshClient) Start(addr, user, password string) (err error) {
	s.contextLog.Info("清理ssh客户端")
	s.stop()
	s.contextLog.Info("开始启动ssh客户端")
	err = s.start(addr, user, password)
	return
}

func (s *sshClient) stop() {
	//如果已经启动将关闭以前的,重新开始
	if atomic.LoadInt32(&s.status) == 1 {
		if s.conn != nil {
			s.conn.Close()
		}
		if s.p != nil {
			s.p.Write([]byte("exit\n"))
			s.p.Close()
		}
	}
	atomic.StoreInt32(&s.status, 0)
}
func (s *sshClient) start(addr, user, password string) (err error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		err = fmt.Errorf("dial %s 出错 %v", addr, err)
		return
	}
	atomic.StoreInt32(&s.status, 1)

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
	}
	cConn, _, _, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		err = fmt.Errorf("ssh new sshclient 出错 %v", err)
		return
	}
	//开始创建终端通讯管道
	channel, reqs, err := cConn.OpenChannel(s.cfg.Equiment.EndSn, nil)
	if err != nil {
		err = fmt.Errorf("ssh openChannel 出错 %v", err)
		return
	}

	//开始等待服务端传送过来的创建伪终端请求
	var pytReq struct {
		Term   string
		Width  uint32
		Heigth uint32
	}
	for req := range reqs {
		if req.Type != "pty-req" {
			req.Reply(false, nil)
		} else {
			if err = ssh.Unmarshal(req.Payload, &pytReq); err != nil {
				err = fmt.Errorf("解析伪终端请求失败 %v", err)
				return
			}
			req.Reply(true, nil)
			break
		}
	}
	if pytReq.Term == "" {
		err = fmt.Errorf("未能获取正确报文")
		return
	}
	//开始建立伪终端
	env := os.Environ()
	cmd := exec.Command("/bin/bash")
	cmd.Env = env
	p, err := pty.Start(cmd)
	if err != nil {
		err = fmt.Errorf("启动伪终端失败 %v", err)
		return
	}
	s.p = p
	term := terminal.NewTerminal(p, "")
	term.SetSize(int(pytReq.Width), int(pytReq.Heigth))
	//开始等待服务端shell请求
	for req := range reqs {
		if req.Type != "shell" {
			req.Reply(false, nil)
		} else {
			req.Reply(true, nil)
			break
		}
	}
	go func() {
		if _, err := io.Copy(channel, p); err != nil {
			s.contextLog.WithField("msg", "copy(channel, p)").Errorln(err)
			return
		}
	}()
	go func() {
		if _, err := io.Copy(p, channel); err != nil {
			s.contextLog.WithField("msg", "copy(p, channel)").Errorln(err)
			return
		}
	}()
	return
}
