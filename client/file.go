package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"time"
)

type fileInfo struct {
	Code    string
	Msg     string
	URL     string
	Version string
	MD5     string
}

//获取流程：首先获取最新版本号，然后下载文件。
//下载的文件会保存在当前目录下, 文件名为时间戳.db
func (b *BoxControl) getDbFileAndReplace() (err error) {
	request, err := http.NewRequest("POST", "http://"+b.cfg.Update.Addr+"/dbfile", nil)
	if err != nil {
		err = fmt.Errorf("POST %s 出错 %v", request.RequestURI, err)
		return
	}
	request.AddCookie(&http.Cookie{Name: "account", Value: b.cfg.Secure.Account})
	request.AddCookie(&http.Cookie{Name: "token", Value: b.cfg.Secure.EpeToken})
	request.AddCookie(&http.Cookie{Name: "verify", Value: b.cfg.Secure.EpeVerify})
	request.AddCookie(&http.Cookie{Name: "endsn", Value: b.cfg.Equiment.EndSn})
	//首先获取当前EndSn下最新数据库版本和MD5
	fileInfo, err := b.getFileInfo(request)
	if err != nil {
		return
	}
	//下载文件
	newName := fmt.Sprintf("%d.db", time.Now().Unix())
	err = b.getFile(request, fileInfo.MD5, newName, true)
	if err != nil {
		return
	}
	err = b.replaceFile("easy.db", newName)
	return
}

func (b *BoxControl) getBinFile(version string) (err error) {
	request, err := http.NewRequest("POST", "http://"+b.cfg.Update.Addr+"/binfile", nil)
	if err != nil {
		err = fmt.Errorf("POST %s 出错 %v", request.RequestURI, err)
		return
	}
	request.AddCookie(&http.Cookie{Name: "GOOS", Value: runtime.GOOS})
	request.AddCookie(&http.Cookie{Name: "GOARCH", Value: runtime.GOARCH})
	request.AddCookie(&http.Cookie{Name: "Version", Value: version})
	request.AddCookie(&http.Cookie{Name: "EndSn", Value: b.cfg.Equiment.EndSn})

	fileInfo, err := b.getFileInfo(request)
	if err != nil {
		return
	}
	//下载文件
	var newName string
	if runtime.GOOS == "windows" {
		newName = "box-new.exe"
	} else {
		newName = "box-new"
	}
	u, err := url.Parse(fileInfo.URL)
	if err != nil {
		err = fmt.Errorf("解析下载链接出错 %v", err)
		return
	}
	request.URL = u
	request.Host = "aliyun.cdn.yireyun.com"
	b.contextLog.WithField("url", fileInfo.URL).Info("开始下载文件")
	err = b.getFile(request, fileInfo.MD5, newName, false)
	if err != nil {
		return
	}
	return
}

//使用Post方法获取指定url返回的信息
func (b *BoxControl) getFileInfo(request *http.Request) (info *fileInfo, err error) {

	resp, err := b.client.Do(request)
	if err != nil {
		err = fmt.Errorf("POST %s 出错 %v", request.RequestURI, err)
		return
	}

	buff, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("POST 读取返回信息出错 %v", err)
		return
	}

	res := new(fileInfo)
	if err = json.Unmarshal(buff, &res); err != nil {
		err = fmt.Errorf("POST 解析返回信息出错 %v", err)
		return
	}
	if res.Code != "0000" {
		err = fmt.Errorf("服务端返回错误[%s]", res.Msg)
		return
	}
	return res, nil
}

//使用指定url下载文件,并对比md5.保存文件为指定文件名
func (b *BoxControl) getFile(request *http.Request, m, newName string, compare bool) (err error) {
	//开始请求数据库文件
	request.Method = "GET"

	resp, err := b.client.Do(request)
	if err != nil {
		err = fmt.Errorf("GET %s 出错 %v", request.URL.RequestURI(), err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("GET %s 返回错误代码 %s", request.URL.RequestURI(), resp.Status)
		return
	}
	buff, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("GET %s 读取返回信息出错 %v", request.URL.RequestURI(), err)
		return
	}
	if compare {
		//对比MD5是否相同
		md, err := hex.DecodeString(m)
		if err != nil {
			err = fmt.Errorf("转换md5 string 到 bytes 出错 %s", err)
			return err
		}
		v := md5.Sum(buff)
		if !bytes.Equal(md, v[:]) {
			err = fmt.Errorf("md5校验失败")
			return err
		}
	}
	if err = ioutil.WriteFile(newName, buff, 0770); err != nil {
		err = fmt.Errorf("写入文件 %s 出错 %v", newName, err)
	}
	return
}

//将srcfile替换为dstfile,并备份srcfile
func (b *BoxControl) replaceFile(srcfile, dstfile string) (err error) {
	newName := fmt.Sprintf("backup/%s.%s", srcfile, time.Now().Format("2006-01-02|15:04:05"))
	buff, err := ioutil.ReadFile(srcfile)
	if err != nil {
		err = fmt.Errorf("读取文件 %s 出错 %v", srcfile, err)
		return
	}
	if err = ioutil.WriteFile(newName, buff, 0770); err != nil {
		err = fmt.Errorf("写入文件 %s 出错 %v", newName, err)
		return
	}
	if err = os.Remove(srcfile); err != nil {
		err = fmt.Errorf("删除文件 %s 出错 %v", srcfile, err)
		return
	}
	if err = os.Rename(dstfile, srcfile); err != nil {
		err = fmt.Errorf("重命名文件 %s -> %s 出错 %v", dstfile, srcfile, err)
	}
	return
}

//将srcfile替换为dstfile,并备份srcfile
func replaceFile(srcfile, dstfile string) (err error) {
	newName := fmt.Sprintf("backup/%s.%s", srcfile, time.Now().Format("2006-01-02|15:04:05"))
	buff, err := ioutil.ReadFile(srcfile)
	if err != nil {
		err = fmt.Errorf("读取文件 %s 出错 %v", srcfile, err)
		return
	}
	if err = ioutil.WriteFile(newName, buff, 0770); err != nil {
		err = fmt.Errorf("写入文件 %s 出错 %v", newName, err)
		return
	}
	if err = os.Rename(dstfile, srcfile); err != nil {
		err = fmt.Errorf("重命名文件 %s -> %s 出错 %v", dstfile, srcfile, err)
	}
	return
}
