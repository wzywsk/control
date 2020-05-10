package main

import (
	"sync/atomic"
	"time"

	"fmt"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

//Session 每个box维护一个session
//如果发现重复的box，原来的box将被替换
type session struct {
	conn   *websocket.Conn
	status int32
	//收到消息将放入这里，如果超过最大缓存将丢弃
	//如果链接断开将重置
	msgBuff    chan []byte
	account    string
	contextLog *logrus.Entry
}

func newSession() *session {
	s := new(session)
	s.contextLog = logrus.WithField("module", "session")
	return s
}

//SetConn 替换原来的conn
func (s *session) SetConn(conn *websocket.Conn, account string) {
	s.conn = conn
	s.account = account
}

//Start 启动后将维持心跳，如果两个心跳周期内收不到心跳报文，
//将断开链接。
func (s *session) Start() {
	go s.start()
}

//Status 获取当前连接状态
func (s *session) Status() int32 {
	status := atomic.LoadInt32(&s.status)
	return status
}

func (s *session) start() {
	if !atomic.CompareAndSwapInt32(&s.status, 0, 1) {
		s.contextLog.Info("已经启动")
		return
	}
	s.msgBuff = make(chan []byte, 1)

	s.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	s.conn.SetPingHandler(func(data string) error {
		s.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		if err := s.conn.WriteMessage(websocket.PongMessage, []byte{}); err != nil {
			s.contextLog.WithField("msg", "websocket 写入心跳").Errorln(err)
		}
		return nil
	})
	for {
		_, msg, err := s.conn.ReadMessage()
		if err != nil {
			s.contextLog.WithField("msg", "websocket ReadMessage").Errorln(err)
			s.Stop()
			return
		}
		select {
		case s.msgBuff <- msg:
		default:
		}
	}
}

//Stop 将链接断开
func (s *session) Stop() {
	if !atomic.CompareAndSwapInt32(&s.status, 1, 0) {
		s.contextLog.Info("已经停止")
		return
	}
	if s.conn != nil {
		s.conn.Close()
	}
}

//WriteMsg 将写入发送信息，读取返回报文，出错将断开链接
func (s *session) WirteMsg(sendCode byte, sendMsg string) (resCode byte, resMsg string, err error) {
	if s.Status() != 1 {
		err = fmt.Errorf("设备离线状态")
		return
	}
	//读取前如果有前一次的信息先清除
	select {
	case <-s.msgBuff:
	default:
	}
	//加包头
	buff := make([]byte, len(sendMsg)+1)
	buff[0] = sendCode
	copy(buff[1:], []byte(sendMsg))
	if err = s.conn.WriteMessage(websocket.TextMessage, buff); err != nil {
		err = fmt.Errorf("websocket WriteMessage 出错 %v", err)
		return
	}
	//从buff读取msg，超时将返回错误
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	var msg []byte
	select {
	case <-timer.C:
		err = fmt.Errorf("等待客户端超时")
		return
	case msg = <-s.msgBuff:
	}
	if len(msg) < 2 {
		err = fmt.Errorf("收到错误报文")
		return
	}
	resCode = msg[0]
	resMsg = string(msg[1:])
	return
}
