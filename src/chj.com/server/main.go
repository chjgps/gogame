package main

import (
	"log"
	"runtime/debug"
	"time"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Println("主进程崩溃: %v", r)
			debug.PrintStack()
		}
		log.Println("----------服务器停止----------")
	}()

	log.Println("----------服务器启动----------")

	NewFacade()
	time.Sleep(time.Hour)
	// 主进程挂起
	for {
		time.Sleep(time.Minute)
	}
}
