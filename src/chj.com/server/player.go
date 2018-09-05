package main

// 客户端对象-处理客户端请求

import (
	"encoding/json"
	"log"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"chj.com/zwwserver/model"

	"golang.org/x/net/websocket"
)

// 与Player进程通信协议
const (
	Player_Init        int16 = 301 + iota // 初始化
	Player_Logout                         // 登出
	Player_Kickout                        // 踢下线
	Player_GetToken                       // 获取token
	Player_RecvMessage                    // 接收客户端消息
	Player_SendMessage                    // 给客户端发消息
)

type Player struct {
	facade    *Facade
	protocol  *Protocol
	sessionRH *RoutineHandle

	// 进程通信Handle
	rh *RoutineHandle

	// 客户端连接
	ws *websocket.Conn

	// 当前所在game房间的通信Handle
	GameRH *RoutineHandle

	// 当前观察的game房间的列表
	watchGames []string

	// 当前token和id
	userToken string
	userId    int

	// 是否被踢下线
	isKickout bool

	retryTime int
}

func NewPlayer(facade *Facade, ws *websocket.Conn) *RoutineHandle {
	this := new(Player)
	this.rh = NewRoutineHandle()
	go this.run()
	facade.protocol.call(this.rh, Player_Init, []interface{}{facade, ws})
	return this.rh
}

func (this *Player) run() {
	defer func() {
		if r := recover(); r != nil {
			log.Println("Player进程崩溃, 3秒后重启: ", r)
			log.Printf("%s", debug.Stack())

			this.retryTime++

			if this.retryTime > 5 {
				log.Println("Player进程崩超过5次, 不再重试!")
			}

			// 休眠一下再重启...
			time.Sleep(time.Second * 3)
			log.Println("Player进程重启!")
			go this.run()
		}
	}()

	for {
		select {
		case recv := <-this.rh.callC:
			cmd, _ := recv.([]interface{})
			switch cmd[0] {
			case Player_Init:
				this.onInit(cmd[1].([]interface{}))
			case Player_Logout:
				this.onLogout()
			case Player_Kickout:
				this.onKickout()
			case Player_GetToken:
				this.onGetToken()
			default:
				log.Println("Player 未处理的命令: ", cmd[0])
				this.rh.callC <- nil
			}
		case recv := <-this.rh.castC:
			cmd, _ := recv.([]interface{})
			switch cmd[0] {
			case Player_RecvMessage:
				this.onRecvMessage(cmd[1].(string))
			case Player_SendMessage:
				this.onSendMessage(cmd[1])
			default:
				log.Println("Player 未处理的命令: ", cmd[0])
			}
		}
	}
}

func (this *Player) onInit(params []interface{}) {
	defer func() {
		this.rh.callC <- nil
	}()

	this.facade = params[0].(*Facade)
	this.protocol = this.facade.protocol
	this.sessionRH = this.facade.sessionRH
	this.ws = params[1].(*websocket.Conn)

	this.watchGames = make([]string, 0)
}

// 接收客户端的消息
func (this *Player) onRecvMessage(message string) {
	// 解析json请求,然后路由处理
	var msgObj map[string]interface{}
	if err := json.Unmarshal([]byte(message), &msgObj); err != nil {
		log.Println("解析客户端消息出错: ", err, message)
		return
	}

	msgType := msgObj["type"].(string)
	switch msgType {
	case "heartBeat": // 心跳包
		this.onHeartBeat(msgObj["data"].(string))
	case "login": // 登陆
		this.onLogin(msgObj["data"].(string))
	case "joinGameRoom": // 加入game房间
		this.onJoinGameRoom(msgObj["data"].(string))
	case "leaveGameRoom": // 离开game房间
		this.onLeaveGameRoom(msgObj["data"].(string))
	case "controlGame": // 控制game
		this.onControlGame(msgObj["data"].(map[string]interface{}))
	case "watchGameRooms": // 从列表观察game状态
		this.onWatchGameRooms(msgObj["data"].([]interface{}))
	case "unwatchGameRooms": // 取消列表观察game状态
		this.onUnwatchGameRooms(msgObj["data"].([]interface{}))
	case "broadcastMessage": // 给房间内的其他玩家广播消息
		this.onBroadcastMessage(msgObj["data"].(string))
	default:
		log.Println("Player未处理的消息类型: ", msgType)
	}
}

// 心跳包
func (this *Player) onHeartBeat(data string) {
	//log.Println("Player.onHeartBeat: ", data)

	// 原样返回
	this.sendMessage("heartBeat", data)
}

// 登陆
func (this *Player) onLogin(userToken string) {
	log.Println("Player.onLogin: ", userToken)
	if userToken == "" {
		this.loginResponse(1, "invalid token1")
		return
	}

	tokenArr := strings.Split(userToken, ":")
	var userId int
	var err error
	if userId, err = strconv.Atoi(tokenArr[0]); err != nil {
		this.loginResponse(1, "invalid token2")
		return
	}

	if userId == 0 {
		this.loginResponse(1, "invalid token3")
		return
	}

	// 使用token进行登录, 从数据库取出token进行校验
	sessionModel := model.NewSessionModel(int32(userId))
	if sessionModel.GetToken() != userToken {
		this.loginResponse(1, "invalid token4")
		return
	}

	this.userToken = userToken
	this.userId = userId

	// 登陆成功, 把自己记录到Session中
	response := this.protocol.call(this.sessionRH, Session_GetPlayer, this.userId)
	if response != nil {
		// 已经在别处登陆了,踢下线
		playerRH := response.(*RoutineHandle)
		if playerRH != nil && !playerRH.destroyed {
			this.protocol.call(playerRH, Player_Kickout, nil)
		}
	}
	this.protocol.call(this.sessionRH, Session_AddPlayer, []interface{}{this.userId, this.rh})

	// 发送登陆成功消息
	this.loginResponse(0, "ok")
}

func (this *Player) loginResponse(code int, message string) {
	response := make(map[string]interface{})
	response["code"] = code
	response["message"] = message
	this.sendMessage("login", response)
}

func (this *Player) onLogout() {
	log.Println("Player.onLogout", this.userId)
	defer func() {
		this.rh.callC <- nil
	}()

	if this.userId > 0 {
		// 从当前game房间退出
		if this.GameRH != nil && !this.GameRH.destroyed {
			this.protocol.call(this.GameRH, Game_Leave, this.userId)
			this.GameRH = nil
		}

		// 清除自己的所有订阅消息
		this.protocol.call(this.sessionRH, Session_ClearGameSubs, this.userId)

		// 从会话管理器删除自己
		if !this.isKickout {
			this.protocol.call(this.sessionRH, Session_RemovePlayer, this.userId)
		}
	}

	// 清理资源
	this.rh.destroy()
}

func (this *Player) onKickout() {
	log.Println("Player.onKickout: ", this.userId)
	defer func() {
		this.rh.callC <- nil
	}()

	// 从会话管理器删除自己
	this.protocol.call(this.sessionRH, Session_RemovePlayer, this.userId)

	this.isKickout = true
	this.ws.Write([]byte{'k', 'i', 'c', 'k', 'o', 'u', 't'})
	this.ws.Close()
}

func (this *Player) onGetToken() {
	log.Println("Player.onGetToken: ", this.userToken)

	this.rh.callC <- this.userToken
}

func (this *Player) onControlGame(params map[string]interface{}) {
	controlType := params["controlType"].(string)
	log.Println("Player.onControlGame: ", controlType)

	// 给game发送指令
	if this.GameRH == nil || this.GameRH.destroyed {
		log.Println("Player.onControlGame: not in Game's room!")
		return
	}

	this.protocol.call(this.GameRH, Game_Control, []interface{}{controlType, this.userId})
}

// 加入game房间
func (this *Player) onJoinGameRoom(deviceName string) {
	log.Println("Player.onJoinGameRoom: ", deviceName)

	this.GameRH = this.getGameRH(deviceName)
	if this.GameRH == nil {
		return
	}

	// 加入房间
	this.protocol.call(this.GameRH, Game_Join, []interface{}{this.userId, this.rh})
}

// 离开game房间
func (this *Player) onLeaveGameRoom(deviceName string) {
	log.Println("Player.onLeaveGameRoom: ", deviceName)

	if this.GameRH == nil || this.GameRH.destroyed {
		log.Println("leave but not in room ", deviceName)
		return
	}

	// 离开房间
	this.protocol.call(this.GameRH, Game_Leave, this.userId)
}

func (this *Player) onWatchGameRooms(deviceNames []interface{}) {
	//log.Println("Player.onWatchGameRooms: ", deviceNames)

	// 计算出真正的新增列表,之前不在已有列表中的才算
	newSubList := make([]string, 0, len(deviceNames))
	for _, v := range deviceNames {
		deviceName, _ := v.(string)

		exist := false
		for _, existName := range this.watchGames {
			if existName == deviceName {
				exist = true
				continue
			}
		}

		if !exist {
			newSubList = append(newSubList, deviceName)
		}
	}

	// 记录到自己的订阅列表
	this.watchGames = append(this.watchGames, newSubList...)

	// 增加到Sessiongame观察列表
	this.protocol.call(this.sessionRH, Session_AddGameSubs, []interface{}{this.userId, newSubList})
}

func (this *Player) onUnwatchGameRooms(deviceNames []interface{}) {
	//log.Println("Player.onUnwatchGameRooms: ", deviceNames)

	// 计算出真正的移除列表,之前已在列表中的才算
	removeSubList := make([]string, 0, len(deviceNames))
	for _, v := range deviceNames {
		deviceName := v.(string)

		exist := false
		for _, existName := range this.watchGames {
			if existName == deviceName {
				exist = true
				break
			}
		}

		if exist {
			removeSubList = append(removeSubList, deviceName)
		}
	}

	// 从自己的列表中减去相应的列表
	newSubList := make([]string, 0, len(this.watchGames)-len(removeSubList))
	for _, deviceName := range this.watchGames {
		exist := false
		for _, existName := range removeSubList {
			if existName == deviceName {
				exist = true
				break
			}
		}

		if !exist {
			newSubList = append(newSubList, deviceName)
		}
	}
	this.watchGames = newSubList

	// 删除Session中的订阅信息
	this.protocol.call(this.sessionRH, Session_RemoveGameSubs, []interface{}{this.userId, removeSubList})
}

func (this *Player) onBroadcastMessage(message string) {
	log.Println("Player.onBroadcastMessage: ", message)

	if this.GameRH == nil || this.GameRH.destroyed {
		log.Println("broadcast message but not in room ")
		return
	}

	// 广播消息
	this.protocol.call(this.GameRH, Game_BroadcastMessage, message)
}

// 获取game的RoutineHandle
func (this *Player) getGameRH(deviceName string) *RoutineHandle {
	resp := this.protocol.call(this.sessionRH, Session_GetGame, deviceName)
	if resp == nil {
		log.Println("room not exist: ", deviceName)
		this.sendGameStatus(deviceName, "error")
		return nil
	}
	GameRH := resp.(*RoutineHandle)
	if GameRH == nil || GameRH.destroyed {
		log.Println("room not exist: ", deviceName)
		this.sendGameStatus(deviceName, "error")
		return nil
	}

	return GameRH
}

func (this *Player) onSendMessage(message interface{}) {
	websocket.Message.Send(this.ws, message)
}

// 发送game状态消息
func (this *Player) sendGameStatus(deviceName string, status string) {
	data := make(map[string]interface{})
	data["deviceName"] = deviceName
	data["status"] = "error"
	this.sendMessage("GameStatus", data)
}

// 给客户端发送消息
func (this *Player) sendMessage(msgType string, data interface{}) {
	msgObj := make(map[string]interface{})
	msgObj["type"] = msgType
	msgObj["data"] = data
	message, _ := json.Marshal(msgObj)

	websocket.Message.Send(this.ws, string(message))
}
