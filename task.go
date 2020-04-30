// Copyright 2020 Alexey Krivonogov. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"eonza/lib"
	"eonza/script"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gentee/gentee"
	"github.com/gorilla/websocket"
	"github.com/kataras/golog"
	"github.com/labstack/echo/v4"
)

const (
	WcClose  = iota // close connection
	WcStatus        // change status
	WcStdout        // new line in console
	WcStdbuf        // current output including carriage
)

type WsClient struct {
	StdoutCount int
	Conn        *websocket.Conn
}

type WsCmd struct {
	TaskID  uint32 `json:"taskid"`
	Cmd     int    `json:"cmd"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Time    string `json:"finish,omitempty"`
}

type StdinForm struct {
	Message string `json:"message"`
}

var (
	task       Task
	prevStatus int
	upgrader   websocket.Upgrader
	wsChan     chan WsCmd

	stdoutBuf []string
	iStdout   int

	console *os.File
	cmdFile *os.File
	outFile *os.File

	chStdin  chan []byte
	chStdout chan []byte
	chSystem chan int
	chFinish chan bool

	clients = make(map[uint32]WsClient)
)

func closeTask() {
	var files []string

	for ; iStdout < len(stdoutBuf); iStdout++ {
		out := lib.ClearCarriage(stdoutBuf[iStdout]) + "\r\n"
		if _, err := outFile.Write([]byte(out)); err != nil {
			golog.Error(err)
		}
	}
	cmdFile.Close()
	outFile.Close()
	for _, item := range []string{"trace", "out"} {
		files = append(files, filepath.Join(scriptTask.Header.LogDir,
			fmt.Sprintf("%08x.%s", task.ID, item)))
	}
	output := filepath.Join(scriptTask.Header.LogDir, fmt.Sprintf("%08x.zip", task.ID))

	if err := lib.ZipFiles(output, files); err != nil {
		golog.Error(err)
	} else {
		for _, item := range files {
			os.Remove(item)
		}
	}
}

func sendStdout(client WsClient) error {
	for i := client.StdoutCount; i < iStdout; i++ {
		if err := client.Conn.WriteJSON(WsCmd{
			TaskID:  task.ID,
			Cmd:     WcStdout,
			Message: stdoutBuf[i],
		}); err != nil {
			return err
		}
	}
	return client.Conn.WriteJSON(WsCmd{
		TaskID:  task.ID,
		Cmd:     WcStdbuf,
		Message: lib.ClearCarriage(stdoutBuf[iStdout]),
	})
}

func initTask() script.Settings {
	var err error

	task = Task{
		ID:        scriptTask.Header.TaskID,
		UserID:    scriptTask.Header.UserID,
		Status:    TaskActive,
		Name:      scriptTask.Header.Name,
		StartTime: time.Now().Unix(),
		Port:      scriptTask.Header.HTTP.Port,
	}
	cmdFile, err = os.OpenFile(filepath.Join(scriptTask.Header.LogDir,
		fmt.Sprintf(`%08x.trace`, task.ID)), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0666)
	if err != nil {
		golog.Fatal(err)
	}
	outFile, err = os.OpenFile(filepath.Join(scriptTask.Header.LogDir,
		fmt.Sprintf(`%08x.out`, task.ID)), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0666)
	if err != nil {
		golog.Fatal(err)
	}
	if _, err = cmdFile.Write([]byte(task.Head())); err != nil {
		golog.Fatal(err)
	}
	console = os.Stdout
	upgrader = websocket.Upgrader{}
	wsChan = make(chan WsCmd)

	chStdin = make(chan []byte)
	chStdout = make(chan []byte)
	chSystem = make(chan int)
	chFinish = make(chan bool)
	stdoutBuf = []string{``}

	go func() {
		var out []byte
		for {
			out = <-chStdout
			mutex.Lock()
			off := len(stdoutBuf) - 1
			lines := strings.Split(string(out), "\n")
			stdoutBuf[off] += lines[0]
			for i := 1; i < len(lines)-1; i++ {
				lines[i] = lib.ClearCarriage(lines[i])
				stdoutBuf = append(stdoutBuf, lines[i])
			}
			if len(lines) > 1 {
				stdoutBuf[off] = lib.ClearCarriage(stdoutBuf[off])
				stdoutBuf = append(stdoutBuf, lines[len(lines)-1])
			}
			for i := off; i < len(stdoutBuf)-1; i++ {
				if _, err := outFile.Write([]byte(stdoutBuf[i] + "\r\n")); err != nil {
					golog.Error(err)
				}
			}
			iStdout = len(stdoutBuf) - 1
			for id, client := range clients {
				if sendStdout(client) == nil {
					client.StdoutCount = iStdout
					clients[id] = client
				} else {
					client.Conn.Close()
					delete(clients, id)
				}
			}
			mutex.Unlock()
		}
	}()

	go func() {
		var cmd WsCmd
		for task.Status <= TaskSuspended {
			cmd = <-wsChan
			mutex.Lock()
			for id, client := range clients {
				err := client.Conn.WriteJSON(cmd)
				if err != nil {
					client.Conn.Close()
					delete(clients, id)
				}
			}
			mutex.Unlock()
		}
		for _, client := range clients {
			client.Conn.Close()
		}
		chFinish <- true
	}()

	return script.Settings{
		ChStdin:  chStdin,
		ChStdout: chStdout,
		ChSystem: chSystem,
	}
}

func wsTaskHandle(c echo.Context) error {
	//	var cmd WsCmd
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	if err = ws.WriteJSON(WsCmd{
		TaskID: task.ID,
		Cmd:    WcStatus,
		Status: task.Status,
	}); err == nil {
		client := WsClient{
			Conn: ws,
		}
		if sendStdout(client) == nil {
			client.StdoutCount = iStdout
			clients[lib.RndNum()] = client
		} else {
			client.Conn.Close()
		}
	}
	/*	defer ws.Close()
		fmt.Println(`Connected`)
		for {
			cmd = <-wsChan
			// Write
			//		err := ws.WriteMessage(websocket.TextMessage, []byte("Hello, Client!"))
			err := ws.WriteJSON(cmd)
			if err != nil {
				// TODO: what's about error?
				fmt.Println(err)
			}
					// Read
					_, msg, err := ws.ReadMessage()
					if err != nil {
						c.Logger().Error(err)
					}
					fmt.Printf("%s\n", msg)
		}*/
	return nil
}

func infoHandle(c echo.Context) error {
	return c.JSON(http.StatusOK, task)
}

func sendCmdStatus(status int, timeStamp int64, message string) {
	taskTrace(timeStamp, status, message)
	jsonValue, err := json.Marshal(TaskStatus{
		TaskID:  task.ID,
		Status:  status,
		Message: message,
		Time:    timeStamp,
	})
	if err == nil {
		resp, err := http.Post(fmt.Sprintf("http://localhost:%d/api/taskstatus",
			scriptTask.Header.ServerPort), "application/json", bytes.NewBuffer(jsonValue))
		if err != nil {
			golog.Error(err)
		} else {
			resp.Body.Close()
		}
	}
	var finish string
	task.Status = status
	if task.Status >= TaskFinished {
		task.FinishTime = timeStamp
		finish = time.Unix(timeStamp, 0).Format(TimeFormat)
	}
	wsChan <- WsCmd{TaskID: task.ID, Cmd: WcStatus, Status: status, Time: finish}
}

func sysHandle(c echo.Context) error {
	cmd, _ := strconv.ParseInt(c.QueryParam(`cmd`), 10, 64)
	id, _ := strconv.ParseInt(c.QueryParam(`taskid`), 10, 64)
	if uint32(id) != task.ID {
		return jsonError(c, fmt.Errorf(`wrong task id`))
	}
	if cmd == gentee.SysTerminate {
		go func() {
			setStatus(TaskTerminated)
			closeTask()
			<-chFinish
			os.Exit(1)
		}()
	}
	if cmd >= gentee.SysSuspend && cmd < gentee.SysTerminate {
		chSystem <- int(cmd)
		switch cmd {
		case gentee.SysSuspend:
			prevStatus = task.Status
			sendCmdStatus(TaskSuspended, time.Now().Unix(), ``)
		case gentee.SysResume:
			sendCmdStatus(prevStatus, time.Now().Unix(), ``)
		}
	}
	return jsonSuccess(c)
}

func setStatus(status int, pars ...interface{}) {
	var message string
	//	cmd := WsCmd{TaskID: task.ID, Cmd: WcStatus, Status: status}
	if len(pars) > 0 {
		message = fmt.Sprint(pars...)
		task.Message = message
	}
	sendCmdStatus(status, time.Now().Unix(), message)

	/*	taskTrace(task.FinishTime, status, task.Message)

		jsonValue, err := json.Marshal(TaskStatus{
			TaskID:  task.ID,
			Status:  task.Status,
			Message: task.Message,
			Time:    task.FinishTime,
		})
		if err == nil {
			resp, err := http.Post(fmt.Sprintf("http://localhost:%d/api/taskstatus",
				scriptTask.Header.ServerPort), "application/json", bytes.NewBuffer(jsonValue))
			if err != nil {
				golog.Error(err)
			} else {
				resp.Body.Close()
			}
		}

	 	wsChan <- cmd */
}

func debug(pars ...interface{}) {
	console.Write([]byte(fmt.Sprintln(pars...)))
}

func stdinHandle(c echo.Context) error {
	var (
		form StdinForm
		err  error
	)
	id, _ := strconv.ParseInt(c.QueryParam(`taskid`), 10, 64)
	if uint32(id) != task.ID {
		return jsonError(c, fmt.Errorf(`wrong task id`))
	}
	if err = c.Bind(&form); err != nil {
		return jsonError(c, err)
	}
	msg := form.Message + "\n"
	chStdin <- []byte(msg)
	chStdout <- []byte(msg)
	return jsonSuccess(c)
}