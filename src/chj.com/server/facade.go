package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
)

// 全局唯一单例

type Facade struct {
	// 协议调用器
	protocol *Protocol

	// 全局配置文件
	config *Config

	// 会话管理器
	sessionRH *RoutineHandle
}

func NewFacade() *Facade {
	this := new(Facade)

	// 协议调用器
	this.protocol = NewProtocol()

	// 全局配置文件
	this.config = NewConfig("config.json")

	// 会话管理器
	this.sessionRH = NewSession(this)

	// 启动服务器
	NewServer(this)

	return this
}

// 调用api接口
func (this *Facade) callApi(service string, method string, params interface{}) interface{} {
	url := this.config.apiRoot + "/" + service + "/" + method
	paramjson0, _ := json.Marshal(params)
	paramjson := string(paramjson0)

	log.Println("callApi-request: ", service, ".", method, "(", paramjson, ")")

	r, err := http.Post(url, "application/json", strings.NewReader(string(paramjson)))
	defer r.Body.Close()
	if err != nil {
		log.Println("callApi error: ", err)
		return nil
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Println("callApi error: ", err)
		return nil

	}

	log.Println("callApi-response: ", string(body))

	var robj map[string]interface{}
	err = json.Unmarshal(body, &robj)
	if err != nil {
		log.Println("callApi error: ", err)
		return nil
	}

	if err, ok := robj["error"]; ok {
		log.Println("callApi error: ", err)
		return nil
	}

	data, _ := robj["data"]
	return data
}
