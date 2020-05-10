package main

import (
	config "easy/box/boxconfig"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"fmt"

	"encoding/json"

	"os"
	"os/exec"
	"runtime"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
)

const (
	MethodHeart = iota + 1
	MethodPullConfig
	MethodPtyReq
	MethodUpdate
)

//BoxControl 负责更新box配置
//当收到云端推送的消息后，使用云端推送的url，
//下载所需要的配置文件
type BoxControl struct {
	cfg *config.BoxConfig
	//此连接将发心跳保持
	conn   *websocket.Conn
	dialer *websocket.Dialer
	header http.Header
	//下载配置文件将使用http方式
	client *http.Client
	//状态 1为启动成功 0为未启动
	status int32
	quit   chan struct{}
	//ssh client
	sshClient  *sshClient
	contextLog *log.Entry
}

//NewBoxControl ...
func NewBoxControl(cfg *config.BoxConfig) *BoxControl {
	b := new(BoxControl)
	b.cfg = cfg
	b.client = new(http.Client)
	b.client.Timeout = 5 * time.Minute
	b.header = make(http.Header)
	b.dialer = new(websocket.Dialer)
	b.sshClient = newSSHClient(cfg)
	b.dialer.NetDial = func(network, addr string) (conn net.Conn, err error) {
		return net.DialTimeout(network, addr, 5*time.Second)
	}
	b.contextLog = logrus.WithFields(log.Fields{})
	return b
}

//Start 开启服务将连接云端
//将在一定时间间隔内发送心跳到云端
//链接断开后将不断重连
func (b *BoxControl) Start() (err error) {
	//初始化websocket需要的信息
	return b.start()
}

func (b *BoxControl) start() (err error) {
	if !atomic.CompareAndSwapInt32(&b.status, 0, 1) {
		err = fmt.Errorf("程序已经启动")
		return err
	}
	if err := b.dial(); err != nil {
		err = fmt.Errorf("连接云端失败[%v]", err)
		return err
	}
	b.quit = make(chan struct{})
	go b.heartbeat()
	b.poll()
	return nil
}

func (b *BoxControl) stop() (err error) {
	if !atomic.CompareAndSwapInt32(&b.status, 1, 0) {
		err = fmt.Errorf("程序已经停止")
		return
	}
	if b.conn != nil {
		b.conn.Close()
	}
	close(b.quit)
	return nil
}

func (b *BoxControl) reconnect() (err error) {
	if err = b.stop(); err != nil {
		return
	}

	if err = b.start(); err != nil {
		return
	}
	return nil
}

func (b *BoxControl) dial() error {

	//重新加载配置
	c := fmt.Sprintf("account=%s; token=%s; verify=%s; endsn=%s",
		b.cfg.Secure.Account, b.cfg.Secure.EpeToken, b.cfg.Secure.EpeVerify, b.cfg.Equiment.EndSn)
	b.header.Add("Cookie", c)
	var delay time.Duration
	for {
		b.contextLog.Infof("开始连接云端 %s", b.cfg.Update.Addr)
		conn, _, err := b.dialer.Dial("ws://"+b.cfg.Update.Addr+"/control", b.header)
		if err == nil {
			b.contextLog.Info("连接云端成功")
			b.conn = conn
			return nil
		}
		if delay == 0 {
			delay = time.Second
		} else {
			delay *= 2
		}
		if delay > 64*time.Second {
			delay = 64 * time.Second
		}
		b.contextLog.Infof("连接云端失败 %v, [%.0f]秒后重连\n", err, delay.Seconds())
		time.Sleep(delay)
	}
}

// 不断读取服务端传下来的报文
func (b *BoxControl) poll() {
	for {
		code, msg, err := b.read()
		if err != nil {
			b.contextLog.WithField("msg", "读取报文出错,开始重连").Errorln(err)
			if err = b.reconnect(); err != nil {
				b.contextLog.WithField("msg", "重连出错").Errorln(err)
				continue
			}
		}
		b.process(code, msg)
	}
}

// 每10秒发送一个心态报文
// 如果2个心跳时长内没有收到报文则断开重连
func (b *BoxControl) heartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	b.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	b.conn.SetPongHandler(func(data string) error {
		b.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		return nil
	})
	for {
		select {
		case <-ticker.C:
			if err := b.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				b.contextLog.WithField("msg", "写入心跳报文出错").Errorln(err)
			}
		case <-b.quit:
			return
		}
	}
}

/*
	包结构
	包类型1字节 + 包体
*/
func (b *BoxControl) read() (code byte, msg string, err error) {
	_, buff, err := b.conn.ReadMessage()
	if err != nil {
		err = fmt.Errorf("websocket ReadMessage 出错 %v", err)
		return
	}
	//读取包类型
	code = buff[0]
	//读取包体
	msg = string(buff[1:])
	return
}

//报文类型 1心跳报文 2拉取配置报文，3发送盒子代码报文
func (b *BoxControl) process(code byte, msg string) {
	switch code {
	case MethodPullConfig:
		contextLog := b.contextLog.WithField("operate", "拉取配置")
		contextLog.Info("收到报文")
		if err := b.PullConfigAndUpdate(); err != nil {
			contextLog.Errorln(err)
			b.writeMsg(MethodPullConfig, err.Error())
			return
		}
		if err := b.writeMsg(MethodPullConfig, "0000"); err != nil {
			contextLog.WithField("msg", "写入返回").Errorln(err)
		}
	case MethodPtyReq:
		contextLog := b.contextLog.WithField("operate", "建立终端请求")
		contextLog.Info("收到报文")
		var req struct {
			Addr     string
			User     string
			Password string
		}
		if err := json.Unmarshal([]byte(msg), &req); err != nil {
			contextLog.WithField("msg", "解析请求").Errorln(err)
			b.writeMsg(MethodPtyReq, err.Error())
			return
		}
		if err := b.sshClient.Start(req.Addr, req.User, req.Password); err != nil {
			contextLog.WithField("msg", "启动终端").Errorln(err)
			b.writeMsg(MethodPtyReq, err.Error())
			return
		}
		if err := b.writeMsg(MethodPtyReq, "0000"); err != nil {
			contextLog.WithField("msg", "写入返回").Errorln(err)
		}
	case MethodUpdate:
		contextLog := b.contextLog.WithField("operate", "更新程序请求")
		contextLog.Info("收到报文")
		var req struct {
			Version string
		}
		if err := json.Unmarshal([]byte(msg), &req); err != nil {
			contextLog.WithField("msg", "解析请求").Errorln(err)
			b.writeMsg(MethodPtyReq, err.Error())
			return
		}
		if err := b.update(req.Version); err != nil {
			contextLog.WithField("msg", "更新程序").Errorln(err)
			b.writeMsg(MethodUpdate, err.Error())
			return
		}
		if err := b.writeMsg(MethodUpdate, "0000"); err != nil {
			contextLog.WithField("msg", "写入返回").Errorln(err)
		}
	}
}
func (b *BoxControl) update(version string) (err error) {
	if err = b.getBinFile(version); err != nil {
		return
	}
	var name string
	if runtime.GOOS == "windows" {
		name = "./box.exe"
	} else {
		name = "./box"
	}
	cmd := exec.Command(name, "--update")
	cmd.Env = os.Environ()
	if err = cmd.Start(); err != nil {
		err = fmt.Errorf("启动 %s --update 出错 %v", name, err)
		return
	}
	return
}

//PullConfigAndUpdate 从服务端下载最新配置文件并加载
func (b *BoxControl) PullConfigAndUpdate() (err error) {
	if err = b.PullConfig(); err != nil {
		return
	}
	//err = b.loader.LoadCore()
	return
}

//PullConfig 从服务下载最新的配置文件
func (b *BoxControl) PullConfig() (err error) {
	if err = b.getDbFileAndReplace(); err != nil {
		return
	}
	return
}

func (b *BoxControl) writeMsg(code byte, msg string) (err error) {
	buff := make([]byte, len(msg)+1)
	buff[0] = code
	copy(buff[1:], []byte(msg))
	if err = b.conn.WriteMessage(websocket.TextMessage, buff); err != nil {
		err = fmt.Errorf("websocket WriteMessage 出错 %v", err)
	}
	return
}
