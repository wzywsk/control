package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/gocarina/gocsv"
)

type ScheduledTask struct {
	HostName                     string `csv:"HostName"`
	TaskName                     string `csv:"TaskName"`
	NextRunTime                  string `csv:"Next Run Time"`
	Status                       string `csv:"Status"`
	LogonMode                    string `csv:"Logon Mode"`
	LastRunTime                  string `csv:"Last Run Time"`
	LastResult                   string `csv:"Last Result"`
	Author                       string `csv:"Author"`
	TaskToRun                    string `csv:"Task To Run"`
	StartIn                      string `csv:"Start In"`
	Comment                      string `csv:"Comment"`
	ScheduledTaskState           string `csv:"Scheduled Task State"`
	IdleTime                     string `csv:"Idle Time"`
	PowerManagement              string `csv:"Power Management"`
	RunAsUser                    string `csv:"Run As User"`
	DeleteTaskIdNotRescheduled   string `csv:"Delete Task If Not Rescheduled"`
	StopTaskIfRunsXHoursAndXMins string `csv:"Stop Task If Runs X Hours and X Mins"`
	Schedule                     string `csv:"Schedule"`
	ScheduleType                 string `csv:"Schedule Type"`
	StartTime                    string `csv:"Start Time"`
	StartDate                    string `csv:"Start Date"`
	EndDate                      string `csv:"End Date"`
	Days                         string `csv:"Days"`
	Months                       string `csv:"Months"`
	RepeatEvery                  string `csv:"Repeat: Every"`
	RepeatUntilTime              string `csv:"Repeat: Until: Time"`
	RepeatUntilDuration          string `csv:"Repeat: Until: Duration"`
	RepeatStopIfStillRunning     string `csv:"Repeat: Stop If Still Running"`
}

func getTaskName(taskName string) (ScheduledTask, error) {
	out, err := exec.Command("schtasks", "/query",
		"/v", "/tn", taskName,
		"/fo", "csv").Output()
	if err != nil {
		return ScheduledTask{}, errors.New(
			fmt.Sprintf("Task '%s' not found", taskName))
	}

	tasks := []ScheduledTask{}
	err = gocsv.Unmarshal(bytes.NewReader(out), &tasks)
	if err != nil {
		return ScheduledTask{}, err
	}

	return tasks[0], nil
}

func update() {
	file, err := os.Create("./boxupdate.log")
	if err != nil {
		log.Println(err)
		return
	}

	log.SetFlags(log.Lshortfile)
	log.SetOutput(file)
	defer func() {
		file.Close()
		os.Exit(0)
	}()

	//先关闭以前的box
	log.Println("开始关闭box")
	cmd := exec.Command("schtasks", "/end", "/tn", "box")
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		log.Println(err)
		return
	}
	log.Println("关闭box成功")
	//替换并备份原来的文件
	log.Println("开始替换和备份文件")
	if err := replaceFile("box.exe", "box-new.exe"); err != nil {
		log.Println(err)
		return
	}
	log.Println("替换和备份文件成功")
	//启动box
	log.Println("开始停止box")
	cmd = exec.Command("schtasks", "/run", "/tn", "box")
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		log.Println(err)
		return
	}
	log.Println("停止box成功")
}
