package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"
)

//下载配置文件handler
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	contextLog := s.contextLog.WithField("func", "下载配置文件")
	if err := r.ParseForm(); err != nil {
		contextLog.Errorf("ParseForm 出错 %v", err)
		return
	}
	endsn := r.FormValue("endsn")
	if endsn == "" {
		contextLog.Errorf("未能取到正确endsn")
		return
	}
	//读取配置文件
	filename := fmt.Sprintf("./file/%s/easy.db", endsn)
	buff, err := ioutil.ReadFile(filename)
	if err != nil {
		contextLog.Errorf("读取db文件出错 %v", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=easy.db")
	w.Write(buff)
	return
}

//上传配置文件handler
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	contextLog := s.contextLog.WithField("func", "上传文件")
	var err error
	defer func() {
		if err != nil {
			w.Write([]byte("上传失败"))
		} else {
			w.Write([]byte("上传成功"))
		}
	}()
	endsn := r.FormValue("endsn")
	if endsn == "" {
		contextLog.Errorf("未能取到正确endsn")
		return
	}
	file, _, err := r.FormFile("easy.db")
	if err != nil {
		contextLog.Errorf("r.FormFile %v", err)
		return
	}
	//如果没有则新建
	path := fmt.Sprintf("./file/%s", endsn)
	if err = os.Mkdir(path, 0755); err != nil {
		contextLog.Errorf("上传文件 建立文件夹出错 %v", err)
	}
	//备份原文件,然后替换
	filename := fmt.Sprintf("./file/%s/easy.db", endsn)
	newname := fmt.Sprintf("./file/%s/%s.db", endsn, time.Now().Format("2006-01-02 15:04:05"))
	if err = os.Rename(filename, newname); err != nil {
		contextLog.Errorf("上传文件 重命名文件出错 %v", err)
	}

	buff, err := ioutil.ReadAll(file)
	if err != nil {
		contextLog.Errorf("上传文件 读取上传文件出错 %v", err)
		return
	}
	if err = ioutil.WriteFile(filename, buff, 0660); err != nil {
		contextLog.Errorf("上传文件 写入文件出错 %v", err)
		return
	}
	//更改所有者 easy:root
	if err = os.Chown(filename, 500, 0); err != nil {
		contextLog.Errorf("上传文件 修改文件所有者出错 %v", err)
	}
	//建立硬连接,供编辑使用
	newname = fmt.Sprintf("./file/hardlink/%s.db", endsn)
	//删除原来的
	if err = os.Remove(newname); err != nil {
		contextLog.Errorf("上传文件 删除硬链接出错 %v", err)
	}
	if err = os.Link(filename, newname); err != nil {
		contextLog.Errorf("上传文件 建立硬链接出错 %v", err)
	}
	return
}
