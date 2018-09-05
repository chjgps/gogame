package main

import (
	"log"
	"time"
)

// 进程handle
type RoutineHandle struct {
	destroyed bool
	callC     chan interface{} // 用于同步调用进程的channel
	castC     chan interface{} // 用于异步调用进程的channel
}

func NewRoutineHandle() *RoutineHandle {
	this := new(RoutineHandle)
	this.callC = make(chan interface{})
	this.castC = make(chan interface{}, 512)
	return this
}

func (this *RoutineHandle) destroy() {
	if this.destroyed {
		return
	}
	//close(this.callC)
	//close(this.castC)
	this.destroyed = true
}

type Protocol struct {
}

func NewProtocol() *Protocol {
	this := new(Protocol)
	return this
}

// 同步调用指定进程,等待返回值,timeout超时时间
func (this *Protocol) call(handle *RoutineHandle, protocol int16, params interface{}) interface{} {
	return this.callT(handle, protocol, params, 500)
}

func (this *Protocol) callT(handle *RoutineHandle, protocol int16, params interface{}, timeout time.Duration) interface{} {
	if handle.destroyed {
		log.Println("protocol.Call routine already destroyed: ", protocol, ", ", params)
		return nil
	}

	select {
	case handle.callC <- []interface{}{protocol, params}:
	case <-time.After(timeout * time.Millisecond):
		return nil
	}

	var ret interface{}
	select {
	case ret = <-handle.callC:
	case <-time.After(timeout * time.Millisecond):
		log.Println("protocol.Call timeout ret <- ch protocol: ", protocol, ", ", params)
		return nil
	}
	return ret
}

// 异步调用指定进程,不等待返回值
func (this *Protocol) cast(handle *RoutineHandle, protocol int16, params interface{}) {
	if handle.destroyed {
		log.Println("protocol.Cast routine already destroyed: ", protocol, ", ", params)
		return
	}
	handle.castC <- []interface{}{protocol, params}
}
