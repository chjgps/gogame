package main

// 服务器端
// 1. 处理客户端程序的请求
// 2. 处理game程序的请求
// 3. 处理api调用请求

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"

	"golang.org/x/net/websocket"
)

type Server struct {
	facade    *Facade
	config    *Config
	protocol  *Protocol
	sessionRH *RoutineHandle

	Games []*Game // 当前在线的game列表
	players []*Player // 当前在线的客户端列表
}

func NewServer(facade *Facade) {
	this := new(Server)

	this.facade = facade
	this.config = this.facade.config
	this.protocol = this.facade.protocol
	this.sessionRH = this.facade.sessionRH

	// 启动websocket监听
	go this.run()
}

func (this *Server) run() {
	defer func() {
		if r := recover(); r != nil {
			log.Println("Server进程崩溃: ", r)
			log.Printf("%s", debug.Stack())
			log.Println("Server进程重启!")
			go this.run()
		}
	}()

	// 启动http监听
	go func() {
		// Http路由设置
		http.HandleFunc("/api", this.apiHandler)
		err := http.ListenAndServe(this.config.httpHost, nil)
		if err != nil {
			log.Println("创建Http监听失败: ", err)
		}
	}()

	// 启动socket监听
	err := http.ListenAndServe(this.config.socketHost, websocket.Server{websocket.Config{}, nil, this.socketHandler})
	if err != nil {
		log.Println("创建WebSocket监听失败: ", err)
		return
	}
}

// WebSocket连接处理器
func (this *Server) socketHandler(ws *websocket.Conn) {
	// 路由
	path := ws.Request().URL.Path
	switch path {
	case "/player":
		this.playerHandler(ws)
	case "/Game":
		this.GameHandler(ws)
	default:
		log.Println("Unknown WebSocket path: ", path)
	}
}

// 客户端连接处理器
func (this *Server) playerHandler(ws *websocket.Conn) {
	//log.Println("Server.playerHandler")

	// 创建一个player对象
	playerRH := NewPlayer(this.facade, ws)

	for {
		var message string
		if err := websocket.Message.Receive(ws, &message); err != nil {
			if err != io.EOF {
				log.Println("playerHandler Receive error", err)
			}
			break
		}

		//log.Println("playerHandler-recv: ", message)

		// 调用player处理消息
		this.protocol.cast(playerRH, Player_RecvMessage, message)
	}

	// 连接断开, 调用player logout
	this.protocol.call(playerRH, Player_Logout, nil)
}

// game连接处理器
func (this *Server) GameHandler(ws *websocket.Conn) {
	//log.Println("Server.GameHandler")

	// 创建一个Game对象
	GameRH := NewGame(this.facade, ws)

	for {
		var message string
		if err := websocket.Message.Receive(ws, &message); err != nil {
			if err != io.EOF {
				log.Println("GameHandler Receive error", err)
				ws.Close()
			}
			break
		}

		//log.Println("GameHandler-recv: ", message)

		// 调用Game处理消息
		this.protocol.cast(GameRH, Game_RecvMessage, message)
	}

	// 连接断开, 调用Game logout
	this.protocol.call(GameRH, Game_Logout, nil)
}

//////////////////////////////////////////////////////////
// http api请求处理 - start

// http api接口处理
func (this *Server) apiHandler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		// 关闭连接
		r.Close = true
		r.Body.Close()
	}()

	message := make([]byte, r.ContentLength)
	r.Body.Read(message)

	//log.Println("apiHandler-recv: ", string(message))

	// 解析json请求,然后路由处理
	var msgObj map[string]interface{}
	if err := json.Unmarshal(message, &msgObj); err != nil {
		log.Println("解析服务器消息出错: ", err, string(message))
		fmt.Fprint(w, this.apiErrorResponse("parse json error"))
		return
	}

	var resp string
	msgType := msgObj["type"].(string)
	switch msgType {
	case "startPlay":
		resp = this.onStartPlay(msgObj["data"].([]interface{}))
	case "getDeviceInfos":
		resp = this.onGetDeviceInfos(msgObj["data"].([]interface{}))
	default:
		log.Println("unknow message type: ", msgType)
		resp = this.apiErrorResponse("unknow message type: " + msgType)
	}

	fmt.Fprint(w, resp)
}

//////////////////////////////////////////////////////////
// 公用方法

// 构造api返回数据
func (this *Server) apiDataResponse(data interface{}) string {
	respobj := make(map[string]interface{})
	respobj["data"] = data
	ret, _ := json.Marshal(respobj)
	return string(ret)
}

func (this *Server) apiErrorResponse(errMsg string) string {
	respobj := make(map[string]interface{})
	respobj["error"] = errMsg
	ret, _ := json.Marshal(respobj)
	return string(ret)
}

// 校验玩家在线token并返回玩家id
func (this *Server) checkOnilneUserToken(userToken string) int {
	if userToken == "" {
		return 0
	}
	tokenArr := strings.Split(userToken, ":")
	var userId int
	var err error
	if userId, err = strconv.Atoi(tokenArr[0]); err != nil || userId == 0 {
		log.Println("Server.checkOnilneUserToken error1: ", err)
		return 0
	}

	// 查询玩家是否在线
	response := this.protocol.call(this.sessionRH, Session_GetPlayer, userId)
	if response == nil {
		log.Println("Server.checkOnilneUserToken: player offline1")
		return 0
	}
	playerRH := response.(*RoutineHandle)
	if playerRH == nil || playerRH.destroyed {
		log.Println("Server.checkOnilneUserToken: player offline2")
		return 0
	}

	// 检查在线玩家token是否一致
	existToken := this.protocol.call(playerRH, Player_GetToken, nil)
	if existToken == nil || existToken.(string) != userToken {
		log.Println("Server.checkOnilneUserToken: invalid token")
		return 0
	}

	return userId
}

//////////////////////////////////////////////////////////
// 具体调用逻辑实现

// 开始游戏
func (this *Server) onStartPlay(params []interface{}) string {
	userToken := params[0].(string)
	deviceName := params[1].(string)
	// 第3个参数，设置是否抓中，目前用于雪暴科技主板通过服务器计算概率的使用
	result := int(0)
	if len(params) > 2 {
		result = int(params[2].(float64))
	}

	log.Println("Server.onStartPlay: ", userToken, ",", deviceName)

	userId := this.checkOnilneUserToken(userToken)
	if userId == 0 {
		log.Println("Server.onStartPlay: invalid token")
		return this.apiErrorResponse("invalid token")
	}

	// 查询game是否在线
	response := this.protocol.call(this.sessionRH, Session_GetGame, deviceName)
	if response == nil {
		log.Println("Server.onStartPlay: device offline1")
		return this.apiErrorResponse("device offline1")
	}
	GameRH := response.(*RoutineHandle)
	if GameRH == nil || GameRH.destroyed {
		log.Println("Server.onStartPlay: device offline2")
		return this.apiErrorResponse("device offline2")
	}

	// 给game发送开始游戏指令
	GameResp := this.protocol.call(GameRH, Game_Control, []interface{}{"start", userId, result}).(string)
	if GameResp != "ok" {
		log.Println("Server.onStartPlay: ", GameResp)
		return this.apiErrorResponse(GameResp)
	}

	return this.apiDataResponse("ok")
}

// 获取设备信息
func (this *Server) onGetDeviceInfos(params []interface{}) string {
	deviceInfos := make(map[string]interface{})

	GameRHs := this.protocol.call(this.sessionRH, Session_GetGames, params)
	if GameRHs == nil {
		log.Println("Server.onGetDeviceInfos: get from session error")
		return this.apiErrorResponse("get from session error")
	}
	for i, GameRH := range GameRHs.([]*RoutineHandle) {
		deviceName := params[i].(string)

		if GameRH != nil && !GameRH.destroyed {
			deviceInfo := this.protocol.call(GameRH, Game_GetDeviceInfo, nil)
			if deviceInfo != nil {
				deviceInfos[deviceName] = deviceInfo
				continue
			}
		}

		// 设备没在线或出错了
		deviceInfo := make(map[string]interface{})
		deviceInfo["deviceName"] = deviceName
		deviceInfo["deviceStatus"] = "error"
		deviceInfo["curPlayer"] = 0
		deviceInfos[deviceName] = deviceInfo
	}

	return this.apiDataResponse(deviceInfos)
}

// http api请求处理 - end
//////////////////////////////////////////////////////////
