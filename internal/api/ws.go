package api

// ws.go — WebSocket 实时推送端点(MRD v2 10.2)。
//
// GET /api/tasks/:id/ws — 建立 WebSocket 连接,实时接收该任务的评测进度事件。
// 事件格式: {"type":"question_start","data":{...}}
//
// 用法(前端):
//   const ws = new WebSocket(`ws://localhost:8080/api/tasks/${taskId}/ws`)
//   ws.onmessage = (e) => { const ev = JSON.parse(e.data); ... }
//
// 连接生命周期:任务完成或断开时自动 unsubscribe。

import (
	"encoding/json"
	"log"
	"net/http"

	"ai-eval-platform/internal/ws"

	"github.com/gin-gonic/gin"
	gorillaWs "github.com/gorilla/websocket"
)

var upgrader = gorillaWs.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许所有来源(开发阶段,生产应限制)
	},
}

// handleTaskWS GET /api/tasks/:id/ws
func handleTaskWS(c *gin.Context) {
	taskID := c.Param("id")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("[ws] client connected for task %s", taskID)

	// 订阅该任务的事件
	ch := ws.GlobalHub.Subscribe(taskID)
	defer ws.GlobalHub.Unsubscribe(taskID, ch)

	// 写协程:从 channel 读事件 → 写 WebSocket
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage() // 只读不处理(客户端发 ping 或 close 时触发)
			if err != nil {
				return
			}
		}
	}()

	// 主循环:读事件 → 推送
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			if err := conn.WriteMessage(gorillaWs.TextMessage, data); err != nil {
				log.Printf("[ws] write error for task %s: %v", taskID, err)
				return
			}
		case <-done:
			return
		}
	}
}
