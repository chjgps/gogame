package main

// game对象-处理game请求

import (
	"encoding/json"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// 与Game进程通信协议
const (
	Game_Init             int16 = 201 + iota // 初始化
	Game_Logout                              // 登出
	Game_Kickout                             // 踢下线
	Game_GetDeviceInfo                       // 获取设备信息
	Game_Join                                // 加入房间
	Game_Leave                               // 离开房间
	Game_BroadcastMessage                    // 广播消息
	Game_Control                             // 发送控制指令
	Game_Watch                               // 列表观察
	Game_Unwatch                             // 取消列表观察
	Game_RecvMessage                         // 接收game消息
)

type Game struct {
	facade    *Facade
	protocol  *Protocol
	sessionRH *RoutineHandle

	// 进程通信Handle
	rh *RoutineHandle

	// 客户端连接
	ws *websocket.Conn

	// 当前token和名称
	GameToken string
	deviceName  string

	// 房间内所有玩家
	players map[int]*RoutineHandle
	// 按加入顺序排列的玩家列表, 先加入的在前面
	playerIds []int

	// game当前状态
	GameStatus string

	// 当前玩家Id
	curPlayerId int

	// 当前玩家是否离开
	isLeave bool

	// 是否被踢下线
	isKickout bool

	// 游戏开始时间
	startTime int64
}

func NewGame(facade *Facade, ws *websocket.Conn) *RoutineHandle {
	this := new(Game)
	this.rh = NewRoutineHandle()
	go this.run()
	facade.protocol.call(this.rh, Game_Init, []interface{}{facade, ws})
	return this.rh
}

func (this *Game) run() {
	defer func() {
		if r := recover(); r != nil {
			log.Println("Game进程崩溃, 3秒后重启: ", r)
			log.Printf("%s", debug.Stack())

			// 休眠一下再重启...
			time.Sleep(time.Second * 3)
			log.Println("Game进程重启!")
			go this.run()
		}
	}()

	for {
		select {
		case recv := <-this.rh.callC:
			cmd, _ := recv.([]interface{})
			switch cmd[0] {
			case Game_Init:
				this.onInit(cmd[1].([]interface{}))
			case Game_Logout:
				this.onLogout()
			case Game_Kickout:
				this.onKickout()
			case Game_GetDeviceInfo:
				this.onGetDeviceInfo()
			case Game_Join:
				this.onJoin(cmd[1].([]interface{}))
			case Game_Leave:
				this.onLeave(cmd[1].(int))
			case Game_BroadcastMessage:
				this.onBroadcastMessage(cmd[1].(string))
			case Game_Control:
				this.onControl(cmd[1].([]interface{}))
			default:
				log.Println("Game 未处理的命令: ", cmd[0])
				this.rh.callC <- nil
			}
		case recv := <-this.rh.castC:
			cmd, _ := recv.([]interface{})
			switch cmd[0] {
			case Game_RecvMessage:
				this.onRecvMessage(cmd[1].(string))
			default:
				log.Println("Game 未处理的命令: ", cmd[0])
			}
		}
	}
}

func (this *Game) onInit(params []interface{}) {
	defer func() {
		this.rh.callC <- nil
	}()

	this.facade = params[0].(*Facade)
	this.protocol = this.facade.protocol
	this.sessionRH = this.facade.sessionRH
	this.ws = params[1].(*websocket.Conn)

	this.GameToken = ""
	this.deviceName = ""

	this.players = make(map[int]*RoutineHandle)
	this.playerIds = make([]int, 0)

	this.GameStatus = "error"
	this.curPlayerId = 0
	this.isLeave = false
	this.isKickout = false
	this.startTime = 0
}

// 接收game的消息
func (this *Game) onRecvMessage(message string) {
	// 解析json请求,然后路由处理
	var msgObj map[string]interface{}
	if err := json.Unmarshal([]byte(message), &msgObj); err != nil {
		log.Println("解析客户端消息出错: ", err, message)
	}

	msgType := msgObj["type"].(string)
	switch msgType {
	case "heartBeat": // 心跳包
		this.onHeartBeat(msgObj["data"].(string))
	case "login": // 登陆
		this.onLogin(msgObj["data"].(string))
	case "status": // 同步状态
		this.onStatus(msgObj["data"].(map[string]interface{}))
	case "result": // 抓取结果
		this.onResult(msgObj["data"].(map[string]interface{}))
	default:
		log.Println("Game未处理的消息类型: ", msgType)
	}
}

// 心跳包
func (this *Game) onHeartBeat(data string) {
	//log.Println("Game.onHeartBeat: ", data)

	// 原样返回
	this.sendMessage("heartBeat", data)
}

// 登陆
func (this *Game) onLogin(GameToken string) {
	log.Println("Game.onLogin: ", GameToken)
	if GameToken == "" {
		this.loginResponse(1, "invalid token1")
		return
	}

	tokenArr := strings.Split(GameToken, ":")
	deviceName := tokenArr[0]

	if deviceName == "" {
		this.loginResponse(1, "invalid token2")
		return
	}

	// TODO 使用token进行登录, 从数据库取出token进行校验
	//	sessionModel := model.NewSessionModel(int32(userId))
	//	if sessionModel.GetToken() != userToken {
	//		this.loginResponse(1, "invalid token4")
	//		return
	//	}

	this.GameToken = GameToken
	this.deviceName = deviceName

	// 登陆成功, 把自己记录到Session中
	response := this.protocol.call(this.sessionRH, Session_GetGame, this.deviceName)
	if response != nil {
		// 已经在别处登陆了,踢下线
		GameRH := response.(*RoutineHandle)
		if GameRH != nil && !GameRH.destroyed {
			this.protocol.call(GameRH, Game_Kickout, nil)
		}
	}
	this.protocol.call(this.sessionRH, Session_AddGame, []interface{}{this.deviceName, this.rh})

	// 发送登陆成功消息
	this.loginResponse(0, "ok")
}

func (this *Game) loginResponse(code int, message string) {
	response := make(map[string]interface{})
	response["code"] = code
	response["message"] = message
	this.sendMessage("login", response)
}

func (this *Game) onStatus(params map[string]interface{}) {
	log.Println("Game.onStatus", params)

	deviceStatus, _ := params["deviceStatus"]
	curPlayer, _ := params["curPlayer"]
	this.GameStatus = deviceStatus.(string)
	this.curPlayerId = int(curPlayer.(float64))

	// 清除离开状态
	if this.curPlayerId == 0 {
		this.isLeave = false
		this.startTime = 0
	}

	// 广播game状态消息
	this.broadcastGameStatus(this.GameStatus, this.curPlayerId)
}

func (this *Game) onResult(params map[string]interface{}) {
	log.Println("Game.onResult", params)

	playResult0, _ := params["playResult"]
	curPlayer0, _ := params["curPlayer"]
	playResult := playResult0.(string)
	curPlayer := int(curPlayer0.(float64))

	if this.curPlayerId != curPlayer {
		log.Println("Game result curPlayer != this.curPlayerId")
		return
	}

	// 调用php服务器记录游戏结果
	resp := this.facade.callApi("play", "GetResult", []interface{}{this.facade.config.apiSecret, this.deviceName, curPlayer, playResult})
	if resp == nil {
		return
	}

	// 通知玩家游戏结果
	this.sendGameResultToCurPlayer(playResult)
}

func (this *Game) onLogout() {
	log.Println("Game.onLogout", this.deviceName)
	defer func() {
		this.rh.callC <- nil
	}()

	if this.deviceName != "" {
		// TODO 其他清理逻辑

		// 广播自己出错状态
		this.broadcastGameStatus("error", 0)

		// 从会话管理器删除自己
		if !this.isKickout {
			this.protocol.call(this.sessionRH, Session_RemoveGame, this.deviceName)
		}
	}

	// 清理资源
	this.rh.destroy()
}

func (this *Game) onKickout() {
	log.Println("Game.onKickout: ", this.deviceName)
	defer func() {
		this.rh.callC <- nil
	}()

	// 从会话管理器删除自己
	this.protocol.call(this.sessionRH, Session_RemoveGame, this.deviceName)

	this.isKickout = true
	this.ws.Close()
}

func (this *Game) onGetDeviceInfo() {
	//log.Println("Game.onGetDeviceInfo: ", this.deviceName)

	deviceInfo := make(map[string]interface{})
	deviceInfo["deviceName"] = this.deviceName
	deviceInfo["deviceStatus"] = this.GameStatus
	deviceInfo["curPlayer"] = this.curPlayerId

	this.rh.callC <- deviceInfo
}

// 玩家加入房间
func (this *Game) onJoin(params []interface{}) {
	log.Println("Game.onJoin: ", params)
	defer func() {
		this.rh.callC <- nil
	}()

	playerId := params[0].(int)
	playerRH := params[1].(*RoutineHandle)

	// 当前游戏玩家返回，取消离开状态
	if this.curPlayerId == playerId {
		this.isLeave = false

		// 给玩家发送游戏已经过去的时间
		passTime := int(time.Now().Unix() - this.startTime)
		this.sendMessageToPlayer(playerRH, "GamePassTime", passTime)
	}

	// 记录到玩家列表中
	this.players[playerId] = playerRH
	this.playerIds = append(this.playerIds, playerId)

	// 给所有房间内玩家广播加入消息(弹幕功能)
	data := make(map[string]interface{})
	data["player"] = playerId
	data["count"] = len(this.playerIds)
	data["players"] = this.getLastPlayerIds()
	this.broadcastPlayerMessage("join", data)
}

// 玩家离开房间
func (this *Game) onLeave(playerId int) {
	log.Println("Game.onLeave: ", playerId)

	defer func() {
		this.rh.callC <- nil
	}()

	// 当前玩家离开状态, 记录离开状态
	if this.curPlayerId == playerId {
		this.isLeave = true
	}

	// 从玩家列表中删除
	delete(this.players, playerId)
	playerIds := make([]int, 0, len(this.playerIds))
	for _, v := range this.playerIds {
		if v != playerId {
			playerIds = append(playerIds, v)
		}
	}
	this.playerIds = playerIds

	// 给所有房间内玩家广播离开消息(弹幕功能)
	data := make(map[string]interface{})
	data["player"] = playerId
	data["count"] = len(this.playerIds)
	data["players"] = this.getLastPlayerIds()
	this.broadcastPlayerMessage("leave", data)
}

func (this *Game) onBroadcastMessage(message string) {
	log.Println("Game.onBroadcastMessage: ", message)
	defer func() {
		this.rh.callC <- nil
	}()

	// 给所有房间内玩家广播离开消息(弹幕功能)
	this.broadcastPlayerMessage("message", message)
}

func (this Game) getLastPlayerIds() []int {
	ids := make([]int, 0, 3)
	for i := len(this.playerIds) - 1; i >= 0; i-- {
		ids = append(ids, this.playerIds[i])
		if len(ids) >= 3 {
			break
		}
	}
	return ids
}

func (this *Game) onControl(params []interface{}) {
	log.Println("Game.onControl: ", params)

	controlType := params[0].(string)
	playerId := params[1].(int)

	switch controlType {
	case "start": // 开始
		// 当前玩家不是自己, 返回busy状态
		if this.curPlayerId > 0 && this.curPlayerId != playerId {
			this.rh.callC <- "busy"
			return
		}

		result := params[2].(int)

		if this.curPlayerId != playerId {
			// 开始逻辑
			this.curPlayerId = playerId
			// 通知game开始
			data := make(map[string]interface{})
			data["controlType"] = controlType
			data["playerId"] = playerId
			data["result"] = result
			this.sendMessage("control", data)
		} else {
			// 重试逻辑
			// 通知game重试
			data := make(map[string]interface{})
			data["controlType"] = "retry"
			data["playerId"] = playerId
			data["result"] = result
			this.sendMessage("control", data)
		}

		// 记录倒计时开始时间
		this.startTime = time.Now().Unix()

		this.rh.callC <- "ok"
	case "catch", "stopmove", "up", "down", "left", "right", "retry", "noretry":
		data := make(map[string]interface{})
		data["controlType"] = controlType
		this.sendMessage("control", data)
		this.rh.callC <- "ok"
	default:
		this.rh.callC <- "unknown control type"
	}
}

// 给所有玩家广播自己的状态
func (this *Game) broadcastGameStatus(deviceStatus string, curPlayer int) {
	// 给房间内的玩家广播状态
	for _, playerRH := range this.players {
		if !playerRH.destroyed {
			this.sendGameStatusToPlayer(playerRH, deviceStatus, curPlayer)
		}
	}

	// 给订阅状态的玩家广播状态
	resp := this.protocol.call(this.sessionRH, Session_GetGameSubs, this.deviceName)
	if resp == nil {
		return
	}
	subPlayerIds, ok := resp.([]int)
	if !ok {
		return
	}
	for _, playerId := range subPlayerIds {
		// 如果在上面已经广播过了, 这里就不再广播了
		exist := false
		for existId, _ := range this.players {
			if playerId == existId {
				exist = true
				break
			}
		}
		if exist {
			continue
		}

		resp := this.protocol.call(this.sessionRH, Session_GetPlayer, playerId)
		if resp == nil {
			continue
		}
		playerRH, ok := resp.(*RoutineHandle)
		if !ok || playerRH.destroyed {
			continue
		}

		this.sendGameStatusToPlayer(playerRH, deviceStatus, curPlayer)
	}
}

// 给房间内的玩家广播玩家消息
func (this *Game) broadcastPlayerMessage(messageType string, message interface{}) {
	log.Println("Game.broadcastPlayerMessage: ", messageType, ", ", message)

	for _, playerRH := range this.players {
		if !playerRH.destroyed {
			this.sendPlayerMessage(playerRH, messageType, message)
		}
	}
}

// 给玩家发送玩家消息
func (this *Game) sendPlayerMessage(playerRH *RoutineHandle, messageType string, message interface{}) {
	data := make(map[string]interface{})
	data["type"] = messageType
	data["data"] = message
	this.sendMessageToPlayer(playerRH, "playerMessage", data)
}

// 给玩家发送game状态消息
func (this *Game) sendGameStatusToPlayer(playerRH *RoutineHandle, deviceStatus string, curPlayer int) {
	// ready/playing/error以外的状态只发送给当前游戏的玩家
	if !(deviceStatus == "ready" || deviceStatus == "playing" || deviceStatus == "error" ||
		this.curPlayerId == curPlayer) {
		return
	}

	data := make(map[string]interface{})
	data["deviceName"] = this.deviceName
	data["deviceStatus"] = deviceStatus
	data["curPlayer"] = curPlayer
	this.sendMessageToPlayer(playerRH, "GameStatus", data)
}

// 给当前玩家发送游戏结果消息
func (this *Game) sendGameResultToCurPlayer(result string) {
	// 查询玩家是否在线
	isOffline := false
	response := this.protocol.call(this.sessionRH, Session_GetPlayer, this.curPlayerId)
	if response == nil {
		log.Println("Game.sendGameResultToCurPlayer: player offline1")
		isOffline = true
	}

	var playerRH *RoutineHandle
	if !isOffline {
		playerRH = response.(*RoutineHandle)
		if playerRH == nil || playerRH.destroyed {
			log.Println("Game.sendGameResultToCurPlayer: player offline2")
			isOffline = true
		}
	}

	if isOffline || this.isLeave {
		// 玩家当前不再房间了, 直接通知game不需要重试了
		data := make(map[string]interface{})
		data["controlType"] = "noretry"
		this.sendMessage("control", data)
	} else {
		this.sendMessageToPlayer(playerRH, "GameResult", result)
	}
}

// 给玩家发送消息
func (this *Game) sendMessageToPlayer(playerRH *RoutineHandle, msgType string, data interface{}) {
	if playerRH == nil || playerRH.destroyed {
		log.Println("Game.sendMessageToPlayer: playerRH is nil or destroyed")
		return
	}

	msgObj := make(map[string]interface{})
	msgObj["type"] = msgType
	msgObj["data"] = data
	message, _ := json.Marshal(msgObj)

	this.protocol.cast(playerRH, Player_SendMessage, string(message))
}

// 给game发送消息
func (this *Game) sendMessage(msgType string, data interface{}) {
	msgObj := make(map[string]interface{})
	msgObj["type"] = msgType
	msgObj["data"] = data
	message, _ := json.Marshal(msgObj)

	websocket.Message.Send(this.ws, string(message))
}
