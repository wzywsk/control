package main

import (
	"easy/cloud/cDb"
	"easy/db"
	"easy/inf/msgNode"
	pro "easy/inf/msgSys/msProB3"
	"easy/ui"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/sirupsen/logrus"
)

const (
	MethodHeart = iota + 1
	MethodPullConfig
	MethodPtyReq
	MethodUpdate
)

//显示盒子在线列表
func (s *Server) showBoxList(w http.ResponseWriter, r *http.Request) {
	//首先验证
	auth := r.Header.Get("Authorization")
	if auth == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="Dotcoo User Login"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	account, passwd, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Dotcoo User Login"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if account != "easy" || passwd != "easy" {
		w.Header().Set("WWW-Authenticate", `Basic realm="Dotcoo User Login"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var trs []string
	for endsn, box := range s.boxs {
		tr := genTr(endsn, box.account, box.Status())
		trs = append(trs, tr)
	}
	page := genPage(trs)
	w.Write([]byte(page))
}

//处理网页传送过来的请求
func (s *Server) method(w http.ResponseWriter, r *http.Request) {
	contextLog := s.contextLog.WithField("func", "网页method接口")
	buff, err := ioutil.ReadAll(r.Body)
	if err != nil {
		contextLog.WithField("msg", "Read r.Body").Errorln(err)
		return
	}
	var req struct {
		Method string
		EndSn  string
	}
	if err = json.Unmarshal(buff, &req); err != nil {
		contextLog.WithField("msg", "解析json").Errorln(err)
		return
	}
	switch req.Method {
	case "genpage":
		err = s.page(req.EndSn)
	case "loadconfig":
		err = s.loadConfig(req.EndSn)
	case "pushconfig":
		err = s.pushConfig(req.EndSn)
	case "ptyreq":
		err = s.ptyReq(req.EndSn)
	case "update":
		err = s.updateBox(req.EndSn)
	default:
		contextLog.Errorf("未找到此方法 %s", req.Method)
		w.Write([]byte("没有这个方法"))
		return
	}
	if err != nil {
		contextLog.WithField("method", req.Method).Errorln(err)
		w.Write([]byte(err.Error()))
		return
	}
	w.Write([]byte("0000"))
}

//更新盒子handler
func (s *Server) updateBox(endsn string) (err error) {
	box, ok := s.boxs[endsn]
	if !ok {
		err = fmt.Errorf("未找到此endsn[%s]", endsn)
		return
	}
	var req struct {
		Version string
	}
	req.Version = "1.0"
	buff, _ := json.Marshal(req)

	go func() {
		_, msg, err := box.WirteMsg(MethodUpdate, string(buff))
		if err != nil {
			s.contextLog.WithFields(logrus.Fields{"operate": "updatebox", "msg": "box.WriteMsg"}).Errorln(err)
			return
		}
		if msg != "0000" {
			s.contextLog.WithFields(logrus.Fields{"operate": "updatebox"}).Errorf("客户端返回错误 %s", msg)
			return
		}
	}()
	err = fmt.Errorf("更新指令已经发送")
	return
}

//推送配置handler
func (s *Server) pushConfig(endsn string) (err error) {
	box, ok := s.boxs[endsn]
	if !ok {
		err = fmt.Errorf("未找到对应终端")
		return
	}
	_, msg, err := box.WirteMsg(MethodPullConfig, "pullconifg")
	if err != nil {
		return
	}
	if msg != "0000" {
		err = fmt.Errorf("%s", msg)
		return
	}
	return
}

//远程调试handler
func (s *Server) ptyReq(endsn string) (err error) {
	box, ok := s.boxs[endsn]
	if !ok {
		err = fmt.Errorf("未找到此endsn[%s]", endsn)
		return
	}
	var res = struct {
		Addr     string
		User     string
		Password string
	}{
		"yireyun.com:10001",
		"easy",
		"easy",
	}
	buff, err := json.Marshal(res)
	if err != nil {
		err = fmt.Errorf("json 打包出错 %v", err)
		return
	}
	_, msg, err := box.WirteMsg(MethodPtyReq, string(buff))
	if err != nil {
		return
	}
	if msg != "0000" {
		err = fmt.Errorf("服务端返回错误[%s]", msg)
		return
	}
	return
}

//生成网页handler
func (s *Server) page(endsn string) (err error) {
	box, ok := s.boxs[endsn]
	if !ok {
		err = fmt.Errorf("未找到此endsn[%s]", endsn)
		return
	}
	p := ui.NewWeb("easy:easy574576@tcp(rdsbai2yuobmen12j0mto502.mysql.rds.aliyuncs.com:3306)/easynode?charset=utf8&timeout=10s")
	if err = p.GeneratePage(endsn, box.account, "../node/webroot/private/"); err != nil {
		err = fmt.Errorf("生成网页出错 %v", err)
		return
	}
	return
}

//DbRunData 存入mongodb中的结构
type DbRunData struct {
	Account    string
	EndSn      string
	ModifyTime time.Time
	Data       []*cDb.EndCfgModuleTag
}

//云端加载handler
func (s *Server) loadConfig(endsn string) (err error) {
	//读取endsn目录下的数据库
	dbfile := fmt.Sprintf("./file/%s/easy.db", endsn)
	dbCfg := db.NewDbRunData(dbfile)
	if err = dbCfg.SqlLoadDbRun(); err != nil {
		return
	}

	box, ok := s.boxs[endsn]
	if !ok {
		err = fmt.Errorf("未找到此endsn[%s]", endsn)
		return
	}
	data := new(DbRunData)
	data.Account = box.account
	data.EndSn = endsn
	data.ModifyTime = time.Now()
	for _, tag := range dbCfg.CfgMdTagVs {
		endTag := new(cDb.EndCfgModuleTag)
		endTag.Active = tag.Cur.Active
		endTag.Controls = tag.Cur.Controls
		endTag.CreatedTime = tag.Cur.CreatedTime
		endTag.CreatedUser = tag.Cur.CreatedUser
		endTag.CtrlStats = tag.Cur.CtrlStats
		endTag.CtrlValues = tag.Cur.CtrlValues
		endTag.DataType = tag.Cur.DataType
		endTag.DefValue = tag.Cur.DefValue
		endTag.InstanceCode = tag.Cur.InstanceCode
		endTag.InstanceId = tag.Cur.InstanceId
		endTag.MaxValue = tag.Cur.MaxValue
		endTag.MinValue = tag.Cur.MinValue
		endTag.Relation = tag.Cur.Relation
		endTag.StepValue = tag.Cur.StepValue
		endTag.Subscriptions = tag.Cur.Subscriptions
		endTag.TagAddr = tag.Cur.TagAddr
		endTag.TagAlias = tag.Cur.TagAlias
		endTag.TagCode = tag.Cur.TagCode
		endTag.TagGrp = tag.Cur.TagGrp
		endTag.TagId = tag.Cur.TagId
		endTag.TagName = tag.Cur.TagName
		endTag.TagNote = tag.Cur.TagNote
		endTag.TagType = tag.Cur.TagType
		endTag.UpdatedTime = tag.Cur.UpdatedTime
		endTag.UpdatedUser = tag.Cur.UpdatedUser
		endTag.Version = tag.Cur.Version
		endTag.EndSn = endsn
		data.Data = append(data.Data, endTag)
	}
	session := s.mongodb.Copy()
	defer session.Close()

	if err = session.DB("easy").C("data").Insert(data); err != nil {
		err = fmt.Errorf("mogodb 插入数据出错 %v", err)
		return
	}
	/*
		if err = session.DB("easy").C("data").Find(bson.M{"account": "xiangzi", "endsn": "3box00201705051"}).One(data); err != nil {
			return
		}
	*/
	//通知总线加载
	req := new(pro.LoadConfigMsg)
	req.Account = data.Account
	req.EndSn = data.EndSn
	buf, err := proto.Marshal(req)
	if err != nil {
		err = fmt.Errorf("proto marshal 出错 %v", err)
		return
	}
	out, err := msgNode.MarshalMsg(msgNode.PkgVer_1, msgNode.MethodGet, buf, nil)
	if err != nil {
		err = fmt.Errorf("msgNode Marshal 出错 %v", err)
		return
	}
	res, err := s.emq.Request("G001", out, 5*time.Second)
	if err != nil {
		err = fmt.Errorf("请求服务端 出错 %v", err)
		return
	}
	_, _, res, err = msgNode.UnmarshalMsg(res, nil)
	if err != nil {
		err = fmt.Errorf("msgNode Unmarshal 出错 %v", err)
		return
	}

	ret := new(pro.LoadConfigRet)
	err = proto.Unmarshal(res, ret)
	if err != nil {
		err = fmt.Errorf("proto Unmarshal 出错 %v", err)
		return
	}
	if ret.ErrNo != "0000" {
		err = fmt.Errorf("服务端返回[%v]", ret.ErrMsg)
		return
	}
	return
}

//网页版终端
func (s *Server) sshWeb(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Println(err)
		return
	}
	endsn := r.FormValue("endsn")
	if endsn == "" {
		log.Println("未取到正确endsn")
		return
	}
	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Title</title>
    <link rel="stylesheet" href="http://aliyun.cdn.yireyun.com/share/css/xterm.css" />
    <script src="http://aliyun.cdn.yireyun.com/share/js/xterm.js"></script>
    <script src="http://aliyun.cdn.yireyun.com/share/js/attach.js" ></script>
    <script src="http://aliyun.cdn.yireyun.com/share/js/fit.js" ></script>
    <style>
        body {
            font-family: helvetica, sans-serif, arial;
            font-size: 1em;
            color: #111;
        }

        h1 {
            text-align: center;
        }

        #terminal-container {
            width: 800px;
            height: 450px;
            margin: 0 auto;
            padding: 2px;
        }

        #terminal-container .terminal {
            background-color: #111;
            color: #fafafa;
            padding: 2px;
        }
    </style>
</head>
<body>
<div id="terminal-container"></div>
<script>

    var conn = new WebSocket("ws://yireyun.com:10000/terminal?endsn=%s");
    var term;
    conn.onerror = function () { alert('连接失败') };
    conn.onopen = function () {
        term = new Terminal({
            termName: "xterm-color",
            cols: 108,
            rows: 25,
            cursorBlink: true,
            scrollback: 100,
            tabStopWidth: 4
        });
        term.open(document.getElementById('terminal-container'));
        term.fit();
        term.attach(conn);
        term._initialized = true;
    };
	conn.onclose = function() {
		alert("连接已经断开");
		term.destroy();
	}
</script>
</body>
</html>`, endsn)
	w.Write([]byte(page))
}

func genPage(trs []string) string {
	var table string
	for _, tr := range trs {
		table += tr
		table += "\n"
	}
	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>设备管理平台</title>
    <script type="text/javascript" src="http://apps.bdimg.com/libs/jquery/1.11.3/jquery.min.js"></script>
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/amazeui/2.7.2/css/amazeui.min.css"/>
    <script type="text/javascript">
        //url 请求地址
        //req_data 请求数据，会转换为json
        //callback_data 成功后的回调函数
        //callback_error 失败后的回调函数
        function request(url, req_data, callback_data, callback_error) {
            $.ajax({
                type:"POST",
                dataType:"text", //json
                url: url,
                async: true,
                cache: false,
                processData: false,
                data: JSON.stringify(req_data),
                timeout: 10000,
                success: function (data) {
                    if (callback_data)
                        callback_data(data);
                },
                error: function (jqXHR, textStatus) {
                    var msg = 'statusText=' + jqXHR.statusText + ', textStatus=' + textStatus;
                    if (callback_error)
                        callback_error(msg);
                }
            });
        }

        function sendMsg(method, endsn) {
            var msg = {"Method": method, "EndSn": endsn};
            request("/method", msg, function(data){
                if (data === "0000") {
                    alert('操作成功');
                }else {
                    alert(data);
                }
            }, function (msg) {
                alert("网络出错");
            });
        }

		 function configDb(endsn) {
            window.open("http://www.yireyun.com:10002?db="+endsn+".db", "_blank","top=200,left=400");
        }
		
		function openTerminal(method, endsn) {
            var msg = {"Method": method, "EndSn": endsn};
            request("/method", msg, function(data){
                if (data === "0000") {
					window.open("http://yireyun.com:10000/sshWeb?endsn="+endsn, "_blank","top=200,left=400,width=833,height=470");
                }else {
                    alert(data);
                }
            }, function (msg) {
                alert("网络出错");
            });
		}

		function upload() {
            $("#form1").submit();
            var t = setInterval(function() {
                //获取iframe标签里body元素里的文字。即服务器响应过来的"上传成功"或"上传失败"
                var word = $("iframe[name='frame1']").contents().find("body").text();
                if (word != "") {
                    alert(word);        //弹窗提示是否上传成功
                    clearInterval(t);   //清除定时器
                }
            }, 1000);
        }
    </script>
</head>
<body>
<table class="am-table am-table-bordered am-table-radius am-table-hover am-text-nowrap am-scrollable-horizontal">
    <thead>
    <tr>
        <th>Endsn</th>
        <th>账号</th>
        <th>状态</th>
        <th>配置操作</th>
        <th>远程管理</th>
        <th>文件操作</th>
    </tr>
    </thead>
    <tbody>
    %s
    </tbody>
</table>
<iframe id="iframe1" name="frame1" style="display:none;"></iframe>
</body>
</html>`, table)
	return page
}

//根据endsn和account生成每一行
func genTr(endsn, account string, status int32) string {
	var s string
	if status == 1 {
		s = `<td><span class="am-badge am-badge-success am-round am-text-default">在线</span></td>`
	} else {
		s = `<td><span class="am-badge am-round am-text-default">离线</span></td>`
	}
	temp := fmt.Sprintf(`<tr>
        <td>%s</td>
        <td>%s</td>
        %s
        <td>
            <button class="am-btn am-btn-primary am-btn-sm" onclick="sendMsg('genpage', '%s')">生成网页</button>
            <button class="am-btn am-btn-primary am-btn-sm" onclick="sendMsg('loadconfig', '%s')">云端加载</button>
            <button class="am-btn am-btn-primary am-btn-sm" onclick="sendMsg('pushconfig', '%s')">推送配置</button>
			<a href="/download?endsn=%s" class="am-btn am-btn-primary am-btn-sm" role="button">下载配置文件</a>
            <button class="am-btn am-btn-primary am-btn-sm" onclick="configDb('%s')">编辑配置</button>
		</td>
		<td>
            <button class="am-btn am-btn-primary am-btn-sm" onclick="openTerminal('ptyreq', '%s')">打开终端</button>
            <button class="am-btn am-btn-primary am-btn-sm" onclick="sendMsg('update', '%s')">更新程序</button>
		</td>
		<td>
            <form id="form1" action="/upload?endsn=%s" method="post" enctype="multipart/form-data" target="frame1">
                <input type="file" style="display: inline;width: 200px;" name="easy.db">
				<button class="am-btn am-btn-primary am-btn-xs" onclick="upload();">上传</button>
            </form>
        </td>
    </tr>`, endsn, account, s, endsn, endsn, endsn, endsn, endsn, endsn, endsn, endsn)
	return temp
}
