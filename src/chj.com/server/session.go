package main

// 会话管理器-记录当前在线的玩家和game

import (
	"log"
	"runtime/debug"
	"time"
)

// 与Session进程通信协议
const (
	Session_Init int16 = 101 + iota // 初始化
	// game
	Session_AddGame     // 添加game
	Session_GetGames    // 批量获取game
	Session_GetGame     // 获取game
	Session_RemoveGame  // 删除game
	Session_GetGameSubs // 获取game订阅表
	// 客户端
	Session_AddPlayer        // 添加玩家
	Session_GetPlayer        // 获取玩家
	Session_RemovePlayer     // 删除玩家
	Session_AddGameSubs    // 增加game订阅信息
	Session_RemoveGameSubs // 移除game订阅信息
	Session_ClearGameSubs  // 清理指定玩家所有game订阅信息
)

type Session struct {
	facade   *Facade
	protocol *Protocol

	// 进程通信Handle
	rh *RoutineHandle

	// game列表
	Games map[string]*RoutineHandle
	// 玩家列表
	players map[int]*RoutineHandle

	// game状态观察订阅表
	GameSubMap map[string][]int
}

func NewSession(facade *Facade) *RoutineHandle {
	this := new(Session)
	this.rh = NewRoutineHandle()
	go this.run()
	facade.protocol.call(this.rh, Session_Init, facade)
	return this.rh
}

func (this *Session) run() {
	defer func() {
		if r := recover(); r != nil {
			log.Println("Session进程崩溃, 3秒后重启: ", r)
			log.Printf("%s", debug.Stack())

			// 休眠一下再重启...
			time.Sleep(time.Second * 3)
			log.Println("Session进程重启!")
			go this.run()
		}
	}()

	for {
		select {
		case recv := <-this.rh.callC:
			cmd, _ := recv.([]interface{})
			switch cmd[0] {
			case Session_Init:
				this.onInit(cmd[1])
			// Game
			case Session_AddGame:
				this.onAddGame(cmd[1].([]interface{}))
			case Session_GetGames:
				this.onGetGames(cmd[1].([]interface{}))
			case Session_GetGame:
				this.onGetGame(cmd[1])
			case Session_RemoveGame:
				this.onRemoveGame(cmd[1])
			case Session_GetGameSubs:
				this.onGetGameSubs(cmd[1].(string))
			// player
			case Session_AddPlayer:
				this.onAddPlayer(cmd[1].([]interface{}))
			case Session_GetPlayer:
				this.onGetPlayer(cmd[1])
			case Session_RemovePlayer:
				this.onRemovePlayer(cmd[1])
			case Session_AddGameSubs:
				this.onAddGameSubs(cmd[1].([]interface{}))
			case Session_RemoveGameSubs:
				this.onRemoveGameSubs(cmd[1].([]interface{}))
			case Session_ClearGameSubs:
				this.onClearGameSubs(cmd[1].(int))
			default:
				log.Println("Session 未处理的命令: ", cmd[0])
				this.rh.callC <- nil
			}
		case recv := <-this.rh.castC:
			cmd, _ := recv.([]interface{})
			switch cmd[0] {
			default:
				log.Println("Session 未处理的命令: ", cmd[0])
			}
		}
	}
}

func (this *Session) onInit(params interface{}) {
	defer func() {
		this.rh.callC <- nil
	}()

	this.facade = params.(*Facade)
	this.protocol = this.facade.protocol
	this.Games = make(map[string]*RoutineHandle)
	this.players = make(map[int]*RoutineHandle)

	this.GameSubMap = make(map[string][]int)
}

func (this *Session) onDestroy() {

}

func (this *Session) onAddGame(params []interface{}) {
	//log.Println("Session.onAddGame", params)

	GameId, _ := params[0].(string)
	GameRH, _ := params[1].(*RoutineHandle)
	this.Games[GameId] = GameRH
	this.rh.callC <- nil
}

func (this *Session) onGetGames(params []interface{}) {
	//log.Println("Session.onGetGames", params)

	GameRHs := make([]*RoutineHandle, 0)
	for _, v := range params {
		deviceName := v.(string)
		if GameRH, ok := this.Games[deviceName]; ok {
			GameRHs = append(GameRHs, GameRH)
		} else {
			GameRHs = append(GameRHs, nil)
		}
	}

	this.rh.callC <- GameRHs
}

func (this *Session) onGetGame(params interface{}) {
	//log.Println("Session.onGetGame", params)

	GameId, _ := params.(string)
	if GameRH, ok := this.Games[GameId]; ok {
		this.rh.callC <- GameRH
	} else {
		this.rh.callC <- nil
	}
}

func (this *Session) onRemoveGame(params interface{}) {
	//log.Println("Session.onRemoveGame: ", params)

	GameId, _ := params.(string)
	delete(this.Games, GameId)
	this.rh.callC <- nil
}

func (this *Session) onGetGameSubs(deviceName string) {
	//log.Println("Session.onGetGameSubs: ", deviceName)

	retList := make([]int, 0)
	if subList, ok := this.GameSubMap[deviceName]; ok {
		retList = subList
	}

	//log.Println("Session.onGetGameSubs - resp: ", retList)
	this.rh.callC <- retList
}

func (this *Session) onAddPlayer(params []interface{}) {
	//log.Println("Session.onAddPlayer", params)

	playerId, _ := params[0].(int)
	playerRH, _ := params[1].(*RoutineHandle)
	this.players[playerId] = playerRH
	this.rh.callC <- nil
}

func (this *Session) onGetPlayer(params interface{}) {
	//log.Println("Session.onGetPlayer", params)

	playerId, _ := params.(int)
	playerRH, ok := this.players[playerId]
	if ok {
		this.rh.callC <- playerRH
	} else {
		this.rh.callC <- nil
	}
}

func (this *Session) onRemovePlayer(params interface{}) {
	//log.Println("Session.onRemovePlayer", params)

	playerId, _ := params.(int)
	delete(this.players, playerId)
	this.rh.callC <- nil
}

func (this *Session) onAddGameSubs(params []interface{}) {
	//log.Println("Session.onAddGameSubs", params)
	defer func() {
		this.rh.callC <- nil
	}()

	playerId := params[0].(int)
	deviceNames := params[1].([]string)

	for _, deviceName := range deviceNames {
		subList, ok := this.GameSubMap[deviceName]
		if !ok {
			subList = make([]int, 0)
		}
		this.GameSubMap[deviceName] = append(subList, playerId)
	}

	//log.Println("Session.onAddGameSubs - new map: ", this.GameSubMap)
}

// 清除玩家的所有game订阅
func (this *Session) onRemoveGameSubs(params []interface{}) {
	//log.Println("Session.onRemoveGameSubs", params)
	defer func() {
		this.rh.callC <- nil
	}()

	playerId := params[0].(int)
	deviceNames := params[1].([]string)

	for _, deviceName := range deviceNames {
		if subList, ok := this.GameSubMap[deviceName]; ok {
			newSubList := make([]int, 0, len(subList))
			for _, v := range subList {
				if v != playerId {
					newSubList = append(newSubList, v)
				}
			}
			this.GameSubMap[deviceName] = newSubList
		}
	}

	//log.Println("Session.onRemoveGameSubs - new map: ", this.GameSubMap)
}

func (this *Session) onClearGameSubs(playerId int) {
	//log.Println("Session.onClearGameSubs", playerId)
	defer func() {
		this.rh.callC <- nil
	}()

	// 清理玩家所有的game订阅数据
	for deviceName, subList := range this.GameSubMap {
		newSubList := make([]int, 0, len(subList))
		for _, v := range subList {
			if v != playerId {
				newSubList = append(newSubList, v)
			}
		}
		this.GameSubMap[deviceName] = newSubList
	}
}
