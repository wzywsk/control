package main

import (
	"log"
	"net/http"
	"sync/atomic"

	"encoding/json"

	"fmt"
	"io/ioutil"

	"crypto/md5"
	"encoding/hex"

	"bytes"

	"time"

	"easy/emq"

	"context"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
)

const (
	CDNAddr = "http://aliyun.cdn.yireyun.com"
)

//Server 负责box的更新与加载工作
type Server struct {
	//box连接集合，box连接云端后将会存在在这里
	//主键为endCode
	boxs map[string]*session
	//服务端启动状态
	status int32
	mux    *http.ServeMux
	//websocket服务端默认配置
	upgrad *websocket.Upgrader
	emq    emq.EsMqInf
	//ssh server
	sshServer *sshServer
	//mongodb conn
	mongodb    *mgo.Session
	contextLog *logrus.Entry
}

//NewServer ...
func NewServer() *Server {
	s := new(Server)
	s.boxs = make(map[string]*session)
	s.mux = http.NewServeMux()
	s.upgrad = new(websocket.Upgrader)
	s.upgrad.ReadBufferSize = 10240
	s.upgrad.WriteBufferSize = 10240
	s.contextLog = logrus.WithField("module", "control")
	s.sshServer = newSSHServer()
	s.mux.HandleFunc("/control", s.control)
	s.mux.HandleFunc("/update", s.control)
	s.mux.HandleFunc("/dbfile", s.file)
	s.mux.HandleFunc("/binfile", s.binfile)
	s.mux.HandleFunc("/", s.showBoxList)
	s.mux.HandleFunc("/sshWeb", s.sshWeb)
	s.mux.HandleFunc("/terminal", s.ssh)
	s.mux.HandleFunc("/method", s.method)
	s.mux.HandleFunc("/upload", s.uploadFile)
	s.mux.HandleFunc("/download", s.downloadFile)
	return s
}

//ListenAndServe 接收到box连接时将会存入boxs
func (s *Server) ListenAndServe() (err error) {
	if !atomic.CompareAndSwapInt32(&s.status, 0, 1) {
		s.contextLog.Info("服务已经启动")
		return nil
	}
	defer atomic.StoreInt32(&s.status, 0)
	//初始化消息队列
	/*
		s.emq = esNats.NewNatsConn(time.Second)
		if err = s.emq.Start(nil, "nats://10.116.105.97:4222"); err != nil {
			return
		}
	*/
	//初始化mongodb连接
	/*
		session, err := mgo.Dial("192.168.1.21:27017")
		if err != nil {
			err = fmt.Errorf("连接mogodb出错 %v", err)
			return
		}
		s.mongodb = session
		auth := &mgo.Credential{
			Username: "root",
			Password: "root",
		}
		if err = s.mongodb.Login(auth); err != nil {
			err = fmt.Errorf("登录mongodb出错 %v", err)
			return
		}
		session.SetMode(mgo.Strong, true)
	*/
	//初始化ssh服务器
	go func() {
		if err := s.sshServer.Start(":10001"); err != nil {
			s.contextLog.WithField("msg", "初始化ssh服务器").Errorln(err)
			return
		}
	}()
	return http.ListenAndServe(":10000", s.mux)
}

//负责处理伪终端请求
//这里将根据get参数获取endsn,查找endsn连接和网页进行通信
func (s *Server) ssh(w http.ResponseWriter, r *http.Request) {
	contextLog := s.contextLog.WithField("func", "与网页进行websocket通信")
	if err := r.ParseForm(); err != nil {
		contextLog.WithField("msg", "r.ParseForm").Errorln(err)
		return
	}
	endsn := r.FormValue("endsn")
	if endsn == "" {
		contextLog.WithField("msg", "FormValue(endsn)").Errorln("未能获取正确Endsn")
		return
	}
	conn, err := s.upgrad.Upgrade(w, r, nil)
	if err != nil {
		contextLog.WithField("msg", "websocket Upgrade").Errorln(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, channel := s.sshServer.GetChannel(ctx, endsn)
	if !ok {
		contextLog.WithField("msg", "sshServer GetChannel").Errorln(err)
		return
	}
	go func(conn *websocket.Conn) {
		buff := make([]byte, 10240)
		for {
			n, err := channel.Read(buff)
			if err != nil {
				if conn != nil {
					conn.Close()
				}
				contextLog.WithField("msg", "websocket channel Read").Errorln(err)
				return
			}
			if err = conn.WriteMessage(websocket.TextMessage, buff[:n]); err != nil {
				contextLog.WithField("msg", "websocket conn WriteMessage").Errorln(err)
				return
			}
		}

	}(conn)
	for {
		_, buff, err := conn.ReadMessage()
		if err != nil {
			contextLog.WithField("msg", "websocket conn ReadMessage").Errorln(err)
			return
		}
		_, err = channel.Write(buff)
		if err != nil {
			contextLog.WithField("msg", "sshServer channel Wirte").Errorln(err)
			return

		}
	}
}

//负责推送更新配置信息，使用websocket协议
//如果有新的链接来时将替换老的链接
func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	contextLog := s.contextLog.WithField("func", "box新建连接到这里")
	//校验cookie
	/*
		if err := s.verify(r); err != nil {
			contextLog.WithField("msg", "校验cookie").Errorln(err)
			w.Write([]byte(err.Error()))
			return
		}
	*/
	//获取endsn
	var endsn string
	var account string
	v, err := r.Cookie("endsn")
	if err != nil {
		contextLog.WithField("msg", "r.Cookie(endsn)").Errorln(err)
		return
	}
	endsn = v.Value
	v, err = r.Cookie("account")
	if err != nil {
		contextLog.WithField("msg", "r.Cookie(account)").Errorln(err)
		return
	}
	account = v.Value

	conn, err := s.upgrad.Upgrade(w, r, nil)
	if err != nil {
		contextLog.WithField("msg", "Upgrade(w,r)").Errorln(err)
		return
	}
	contextLog.WithFields(logrus.Fields{"account": account, "endsn": endsn}).Infof("收到连接 %s", conn.RemoteAddr())
	//检查链接是否已经存在，如果存在则替换
	if box, ok := s.boxs[endsn]; ok {
		contextLog.WithFields(logrus.Fields{"account": account, "endsn": endsn, "addr": conn.RemoteAddr()}).Info("连接已存在替换")
		box.Stop()
		box.SetConn(conn, account)
		box.Start()
	} else {
		contextLog.WithFields(logrus.Fields{"account": account, "endsn": endsn, "addr": conn.RemoteAddr()}).Info("连接不存在新建")
		b := newSession()
		b.SetConn(conn, account)
		b.Start()
		s.boxs[endsn] = b
	}
}

func (s *Server) binfile(w http.ResponseWriter, r *http.Request) {
	contextLog := s.contextLog.WithField("func", "下载更新文件")
	//获取GOOS
	var GOOS, GOARCH, Version, endsn string
	v, err := r.Cookie("GOOS")
	if err != nil {
		contextLog.WithField("msg", "r.Cookie(GOOS)").Errorln(err)
		w.Write([]byte(err.Error()))
		return
	}
	GOOS = v.Value
	v, err = r.Cookie("Version")
	if err != nil {
		contextLog.WithField("msg", "r.Cookie(Version)").Errorln(err)
		w.Write([]byte(err.Error()))
		return
	}
	Version = v.Value
	v, err = r.Cookie("GOARCH")
	if err != nil {
		contextLog.WithField("msg", "r.Cookie(Version)").Errorln(err)
		w.Write([]byte(err.Error()))
		return
	}
	GOARCH = v.Value
	v, err = r.Cookie("EndSn")
	if err != nil {
		contextLog.WithField("msg", "r.Cookie(endsn)").Errorln(err)
		w.Write([]byte(err.Error()))
		return
	}
	endsn = v.Value
	//如果是POST方法则为获取文件信息
	if r.Method == "POST" {
		var res struct {
			Code    string
			Msg     string
			URL     string
			Version string
			MD5     string
		}
		var u string
		if GOOS == "windows" {
			u = fmt.Sprintf("%s/updatefile/%s/%s_%s_%s/box.exe", CDNAddr, endsn, GOOS, GOARCH, Version)
		} else {
			u = fmt.Sprintf("%s/updatefile/%s/%s_%s_%s/box", CDNAddr, endsn, GOOS, GOARCH, Version)
		}
		res.Version = Version
		res.URL = u
		res.Code = "0000"
		buff, _ := json.Marshal(res)
		w.Write(buff)
	}
}

//负责处理文件下载，如果是GET请求将返回下载文件
//如果是POST请求将返回最新版本和MD5
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	contextLog := s.contextLog.WithField("func", "下载db文件")
	//首先校验cookie
	/*
		if err := s.verify(r); err != nil {
			contextLog.WithField("msg", "校验Cookie").Errorln(err)
			w.Write([]byte(err.Error()))
			return
		}
	*/
	//获取endsn
	var endsn string
	v, err := r.Cookie("endsn")
	if err != nil {
		contextLog.WithField("msg", "r.Cookie(GOOS)").Errorln(err)
		w.Write([]byte(err.Error()))
		return
	}
	endsn = v.Value
	//如果是GET方法则为下载文件
	if r.Method == "GET" {
		//找到endsn目录下的easy.db返回
		filename := fmt.Sprintf("./file/%s/easy.db", endsn)
		buff, err := s.findFile(filename)
		if err != nil {
			contextLog.WithField("msg", "查找文件").Errorln(err)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(buff)
	}
	//如果是POST方法则为获取文件信息
	if r.Method == "POST" {
		//取出endsn目录下easy.db的MD5值
		var res struct {
			Code    string
			Msg     string
			Version string
			MD5     string
		}
		filename := fmt.Sprintf("./file/%s/easy.db", endsn)
		v, err := s.fileMD5(filename)
		if err != nil {
			log.Println(err)
			res.Code = "9999"
			res.Msg = err.Error()
		} else {
			res.Code = "0000"
			res.Msg = "sucess"
			res.MD5 = v
		}
		buff, _ := json.Marshal(res)
		w.Write(buff)
	}
}

func (s *Server) verify(r *http.Request) (err error) {
	var account, token, verify string
	var v *http.Cookie
	//校验cookie
	if v, err = r.Cookie("account"); err != nil {
		err = fmt.Errorf("获取 account Cookie 错误 %v", err)
		return
	}
	account = v.Value
	if v, err = r.Cookie("token"); err != nil {
		err = fmt.Errorf("获取 token Cookie 错误 %v", err)
		return
	}
	token = v.Value
	if v, err = r.Cookie("verify"); err != nil {
		err = fmt.Errorf("获取 verify Cookie 错误 %v", err)
		return
	}
	verify = v.Value
	err = s.check(account, token, verify)
	return
}

type reqToken struct {
	Account   string `comment:"登录账号"`
	EpeToken  string `comment:"登录令牌"`
	EpeVerify string `comment:"登录校验"`
	EndType   string `comment:"终端类型"`
	EndCode   string `comment:"终端编号"`
}

type resToken struct {
	ErrorNo  string `comment:"错误号"`
	ErrorMsg string `comment:"错误信息"`
}

func (s *Server) check(account, token, verify string) (err error) {
	req := new(reqToken)
	req.Account = account
	req.EpeToken = token
	req.EpeVerify = verify
	buff, err := json.Marshal(req)
	if err != nil {
		err = fmt.Errorf("json 打包错误 %v", err)
		return
	}
	reader := bytes.NewBuffer(buff)
	resp, err := http.Post("http://www.yireyun.com/sso/verifyEpe", "application/json", reader)
	if err != nil {
		err = fmt.Errorf("POST sso/verifyEpe 出错 %v", err)
		return
	}
	buff, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("读取r.Bdoy 出错 %v", err)
		return
	}
	res := new(resToken)
	if err = json.Unmarshal(buff, res); err != nil {
		err = fmt.Errorf("json 解析出错 %v", err)
		return
	}
	if res.ErrorNo != "0000" {
		err = fmt.Errorf("服务器返回错误[%s]", res.ErrorMsg)
	}
	return
}

//文件目录默认为file/{endsn}
//读取指定文件buff
func (s *Server) findFile(filename string) (buff []byte, err error) {
	return ioutil.ReadFile(filename)
}

//读取指定文件的md5值
func (s *Server) fileMD5(filename string) (value string, err error) {
	buff, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}
	v := md5.Sum(buff)
	value = hex.EncodeToString(v[:])
	return
}

func main() {
	s := NewServer()
	log.Fatalln(s.ListenAndServe())
}

func init() {
	t := new(logrus.TextFormatter)
	t.TimestampFormat = "2006-01-02 15:04:05"
	logrus.SetFormatter(t)
}
