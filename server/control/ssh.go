package main

import (
	"fmt"
	"io/ioutil"
	"net"

	"sync"

	"context"

	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

type sshServer struct {
	mu        *sync.Mutex
	chans     map[string]ssh.Channel
	cs        map[string]*ssh.ServerConn
	serconfig *ssh.ServerConfig
	//临时密码,使用过一次以后将重新产生
	tempPasswd map[string]string
	contextLog *logrus.Entry
}

func newSSHServer() *sshServer {
	s := new(sshServer)
	s.mu = new(sync.Mutex)
	s.chans = make(map[string]ssh.Channel)
	s.cs = make(map[string]*ssh.ServerConn)
	s.tempPasswd = make(map[string]string)
	s.contextLog = logrus.WithField("module", "sshServer")
	return s
}

//Start ...
func (s *sshServer) Start(addr string) (err error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		err = fmt.Errorf("net.Listen 出错 %v", err)
		return
	}

	s.serconfig = &ssh.ServerConfig{
		//这里使用临时分配的用户名和密码
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "easy" && string(pass) == "easy" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected for %q", c.User())
		},
	}
	//加载ssh私钥
	privateBytes, err := ioutil.ReadFile("id_rsa")
	if err != nil {
		err = fmt.Errorf("读取私钥出错 %v", err)
		return
	}

	private, err := ssh.ParsePrivateKey(privateBytes)
	if err != nil {
		err = fmt.Errorf("解析私钥出错 %v", err)
		return
	}
	s.serconfig.AddHostKey(private)

	for {
		conn, err := listener.Accept()
		if err != nil {
			err = fmt.Errorf("listener Accept 出错 %v", err)
			return err
		}
		go s.handler(conn)
	}
}

func (s *sshServer) GetChannel(ctx context.Context, endsn string) (ok bool, channel ssh.Channel) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false, nil
		case <-ticker.C:
			s.mu.Lock()
			channel, ok = s.chans[endsn]
			s.mu.Unlock()
			if ok {
				return
			}
		}
	}
}

func (s *sshServer) handler(conn net.Conn) {
	contextLog := s.contextLog.WithField("func", "ssh conn 在这里建立")
	sConn, chans, _, err := ssh.NewServerConn(conn, s.serconfig)
	if err != nil {
		contextLog.WithField("msg", "ssh.NewServerConn").Errorln(err)
		return
	}
	//等待客户端5秒,不创建管道就断开
	var newChannel ssh.NewChannel
	select {
	case newChannel = <-chans:
	case <-time.After(5 * time.Second):
		contextLog.Info("等待客户端超时,连接断开")
		conn.Close()
		return
	}
	//先关闭以前的
	endsn := newChannel.ChannelType()
	s.mu.Lock()
	if conn, ok := s.cs[endsn]; ok {
		if conn != nil {
			conn.Close()
		}
	}
	s.cs[endsn] = sConn
	s.mu.Unlock()

	channel, _, err := newChannel.Accept()
	if err != nil {
		contextLog.WithField("msg", "channel.Accept").Errorln(err)
		return
	}
	//发送创建伪终端请求
	var ptyReq = struct {
		Term   string
		Width  uint32
		Heigth uint32
	}{
		"xterm-color",
		108,
		25,
	}

	f, err := channel.SendRequest("pty-req", true, ssh.Marshal(ptyReq))
	if err != nil {
		contextLog.WithField("msg", "发送创建为终端请求").Errorln(err)
		return
	}
	if !f {
		err = fmt.Errorf("客户端拒绝创建终端")
		return
	}
	f, err = channel.SendRequest("shell", true, nil)
	if err != nil {
		contextLog.WithField("msg", "发送进入shell请求").Errorln(err)
		return
	}
	if !f {
		err = fmt.Errorf("客户端创建shell失败")
		return
	}
	s.mu.Lock()
	s.chans[endsn] = channel
	s.mu.Unlock()
}
