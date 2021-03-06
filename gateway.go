package main

import (
	"bytes"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/sencydai/h5cool/log"
	"github.com/sencydai/h5cool/proto/pack"

	"github.com/sencydai/h5cool/dispatch"
	g "github.com/sencydai/h5cool/gconfig"
	"github.com/sencydai/h5cool/service/actormgr"
	t "github.com/sencydai/h5cool/typedefine"
)

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024 * 10,
		CheckOrigin: func(*http.Request) bool {
			return true
		},
	}

	connCount   uint
	connCountMu sync.Mutex
)

func addConnCount() bool {
	connCountMu.Lock()
	defer connCountMu.Unlock()
	if connCount >= g.GetRealCount() {
		return false
	}
	connCount++
	return true
}

func subConnCount() {
	connCountMu.Lock()
	defer connCountMu.Unlock()
	connCount--
}

func handleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error(err.Error())
		return
	}
	addr := conn.RemoteAddr().String()
	log.Infof("client conn: %s", addr)
	if !addConnCount() {
		conn.Close()
		return
	}

	account := t.NewAccount(conn)

	defer func() {
		if err := recover(); err != nil {
			log.Errorf("client %s panic: %v, %s", addr, err, string(debug.Stack()))
		}

		subConnCount()
		if g.IsGameClose() {
			return
		}

		account.Close()
		dispatch.PushSystemMsg(actormgr.OnAccountLogout, account)
	}()

	var (
		tag     int
		dataLen int
		cmdId   int
	)
	headSize := pack.HEAD_SIZE
	defTag := pack.DEFAULT_TAG
	buff := make([]byte, 0)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil || g.IsGameClose() {
			break
		}
		buff = append(buff, data...)
		if len(buff) < headSize {
			continue
		}

		reader := bytes.NewReader(buff)
		pack.Read(reader, &tag, &dataLen, &cmdId)
		log.Infof("recv data: len(%d),tag(0x%x),dataLen(%d),cmdId(%d)", len(buff), tag, dataLen, cmdId)
		if tag != defTag {
			break
		}
		size := headSize + dataLen
		if len(buff) < size {
			continue
		}
		data = buff[headSize:size]
		buff = buff[size:]
		reader.Reset(data)
		dispatch.PushClientMsg(account, cmdId, reader)
	}
}

func startGateWay() {
	server := http.NewServeMux()
	server.HandleFunc("/", handleConnection)

	if len(g.GameConfig.CertFile) == 0 || len(g.GameConfig.KeyFile) == 0 {
		go http.ListenAndServe(fmt.Sprintf(":%d", g.GameConfig.Port), server)
	} else {
		go http.ListenAndServeTLS(fmt.Sprintf(":%d", g.GameConfig.Port),
			g.GameConfig.CertFile, g.GameConfig.KeyFile, server)
	}

	log.Infof("start gateway: %d", g.GameConfig.Port)
}
