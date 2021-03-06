package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/ioutil"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sencydai/h5cool/base"
	g "github.com/sencydai/h5cool/gconfig"
	"github.com/sencydai/h5cool/log"

	_ "github.com/go-sql-driver/mysql"
)

const (
	appId     = "wx6913c52d7f2bc323"
	appSecret = "5aa01b1b15ffb9c43ff7842fb7822aff"

	loginKey = "QazXswEdc&141009522@"
)

var (
	loginCheck = "https://api.weixin.qq.com/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code"

	accountEngine *sql.DB

	selectStmt  *sql.Stmt
	selectSemap = base.NewSemaphore(5)

	insertStmt  *sql.Stmt
	insertMutex sync.Mutex
)

func onHandleLogin(w http.ResponseWriter, r *http.Request) {
	retCode := make(map[string]interface{})
	retCode["code"] = -1
	defer func() {
		if err := recover(); err != nil {
			log.Fatalf("onlogin error: %v, %s", err, string(debug.Stack()))
		}
		data, _ := json.Marshal(retCode)
		log.Infof("login retcode: %s", string(data))
		w.Write(data)
	}()

	if r.Method != "POST" {
		return
	}
	r.ParseForm()
	log.Infof("login recv: %v", r.PostForm)
	code := r.PostFormValue("code")
	if len(code) == 0 {
		retCode["code"] = -2
		return
	}

	resp, err := http.Get(fmt.Sprintf(loginCheck, appId, appSecret, code))
	if err != nil {
		log.Errorf("login failed: %v", err)
		retCode["code"] = -3
		return
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		retCode["code"] = -4
		return
	}

	values := make(map[string]interface{})
	if err = json.Unmarshal(body, &values); err != nil {
		retCode["code"] = -5
		return
	}
	if _, ok := values["errcode"]; ok {
		log.Errorf("login failed: %s", values["errmsg"].(string))
		retCode["code"] = -6
		return
	}

	openId := values["openid"].(string)
	sessionKey := values["session_key"].(string)
	if len(openId) == 0 || len(sessionKey) == 0 {
		retCode["code"] = -7
		return
	}

	retCode["id"], retCode["level"] = initAccount(openId)
	retCode["timeout"] = time.Now().Add(time.Minute * 5).Unix()
	retCode["code"] = 0

	hash := sha256.New()
	hash.Write([]byte(fmt.Sprintf("%d%d%d%d%s",
		retCode["code"], retCode["id"], retCode["level"], retCode["timeout"], loginKey)))
	retCode["session"] = hash.Sum(nil)
}

func initAccount(openId string) (int, int) {
	if id, gmLevel, err := selectAccount(openId); err == nil {
		return id, gmLevel
	}

	return insertAccount(openId), 0
}

func selectAccount(openId string) (int, int, error) {
	selectSemap.Require()
	defer selectSemap.Release()

	var (
		id      int64
		gmlevel int
	)
	err := selectStmt.QueryRow(openId).Scan(&id, &gmlevel)
	if err != nil && err != sql.ErrNoRows {
		panic(err)
	}
	return int(id), gmlevel, err
}

func insertAccount(openId string) int {
	insertMutex.Lock()
	defer insertMutex.Unlock()

	result, err := insertStmt.Exec(openId, time.Now())
	if err != nil {
		panic(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		panic(err)
	}

	return int(id)
}

func initAccountDB() {
	var err error
	if accountEngine, err = sql.Open("mysql", g.GameConfig.AccountDB); err != nil {
		panic(err)
	} else if err = accountEngine.Ping(); err != nil {
		panic(err)
	}

	go func() {
		for {
			select {
			case <-time.After(time.Hour):
				if err := accountEngine.Ping(); err != nil {
					log.Fatal(err)
				}
			}
		}
	}()

	accountEngine.SetConnMaxLifetime(0)

	selectStmt, err = accountEngine.Prepare("select id,gmlevel from account where openid=?")
	if err != nil {
		panic(err)
	}
	insertStmt, err = accountEngine.Prepare("insert account(openid,createtime,gmlevel) values(?,?,0)")
	if err != nil {
		panic(err)
	}
}

func startLoginServer() {
	initAccountDB()

	server := http.NewServeMux()
	server.HandleFunc("/accountlogin", onHandleLogin)

	if len(g.GameConfig.CertFile) == 0 || len(g.GameConfig.KeyFile) == 0 {
		go http.ListenAndServe(fmt.Sprintf(":%d", g.GameConfig.LoginPort), server)
	} else {
		go http.ListenAndServeTLS(fmt.Sprintf(":%d", g.GameConfig.LoginPort),
			g.GameConfig.CertFile, g.GameConfig.KeyFile, server)
	}

	log.Infof("start login server: %d", g.GameConfig.LoginPort)
}
