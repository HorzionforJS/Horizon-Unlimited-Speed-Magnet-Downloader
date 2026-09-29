package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"horizon/internal/auth"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// 桌面 / Web 客户端跨域；生产环境可按需收紧为白名单域名。
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsProgress 每秒向客户端推送该用户所有任务的实时进度（WebSocket，避免轮询）。
func (s *Server) wsProgress(c *gin.Context) {
	cl := auth.ClaimsFrom(c)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for range tick.C {
		tasks, err := s.store.ListTasks(cl.UserID)
		if err != nil {
			return
		}
		payload := make([]map[string]interface{}, 0, len(tasks))
		for _, t := range tasks {
			item := map[string]interface{}{
				"info_hash": t.InfoHash, "name": t.Name, "status": t.Status,
				"total": t.Total, "completed": t.Completed, "peers": 0, "speed": 0,
			}
			if p, err := s.eng.Progress(t.InfoHash); err == nil {
				item["name"] = p.Name
				item["status"] = p.Status
				item["total"] = p.Total
				item["completed"] = p.Completed
				item["peers"] = p.Peers
				item["speed"] = p.Speed
			}
			payload = append(payload, item)
		}
		if err := conn.WriteJSON(gin.H{"tasks": payload}); err != nil {
			return
		}
	}
}
