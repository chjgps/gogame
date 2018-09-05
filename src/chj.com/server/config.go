package main

// 全局配置文件

import (
	"encoding/json"
	"io/ioutil"

	"chj.com/zwwserver/model"
)

type Config struct {
	// 服务器api调用地址
	apiRoot string
	// 与服务器通信的密钥
	apiSecret string

	// socket监听地址
	socketHost string
	// http监听地址
	httpHost string

	// 数据库配置
	userCountPerDB int
	mysqlConfig    map[string]string
}

func NewConfig(path string) *Config {
	this := new(Config)

	// 读取配置文件
	jsonstr, err := ioutil.ReadFile(path)
	if err != nil {
		this.initWithDefault()
		return this
	}

	// 转换为
	var jsonobj interface{}
	err = json.Unmarshal(jsonstr, &jsonobj)
	if err != nil {
		this.initWithDefault()
		return this
	}

	cfgobj := jsonobj.(map[string]interface{})

	// 读取当前平台, 根据当前平台读取具体平台下的参数
	platform := cfgobj["platform"].(string)
	cfgobj = cfgobj[platform].(map[string]interface{})

	this.apiRoot = cfgobj["apiRoot"].(string)
	this.apiSecret = cfgobj["apiSecret"].(string)
	this.socketHost = cfgobj["socketHost"].(string)
	this.httpHost = cfgobj["httpHost"].(string)

	// 数据库配置,直接写到model中
	model.Config_userCountPerDB = int32(cfgobj["userCountPerDB"].(float64))
	mysqlConfig := cfgobj["mysqlConfig"].(map[string]interface{})
	for key, value := range mysqlConfig {
		model.Config_mysqlConfig[key] = value.(string)
	}

	return this
}

// 初始化为缺省值
func (this *Config) initWithDefault() {
	this.apiRoot = "http://192.168.55.101:30083/api/1.1.0"
	this.apiSecret = "kqlzV1bJLHJ6asF7qPNmcfsjBgPYRt7Tki"

	this.socketHost = ":31401"
	this.httpHost = ":31402"
}
