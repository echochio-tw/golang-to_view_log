package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket" // WebSocket 庫
	tail "github.com/nxadm/tail"  // 日誌追蹤庫
)

// 設定 WebSocket 升級器
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// 允許所有來源連接，或者你可以根據需求限制特定來源
		return true
	},
}

// wsHandler 處理 WebSocket 連接
func wsHandler(w http.ResponseWriter, r *http.Request) {
	var logFilePath string
	if r.URL.Path == "/ws/uwsgi_log" { // 確保這裡仍然檢查 /ws/uwsgi_log
		logFilePath = "/var/log/uwsgi/uwsgi.log"
	} else {
		// 理論上，Nginx 應該只轉發 /ws/uwsgi_log 到這裡
		// 但以防萬一，如果路徑不符，還是返回 404
		http.Error(w, "Invalid log path", http.StatusNotFound) // 使用英文以便於錯誤報告一致
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("Client connected to %s WebSocket", logFilePath)

	// --- WebSocket Ping-Pong 心跳機制 ---
	go func() {
		ticker := time.NewTicker(30 * time.Second) // 每 30 秒發送一次 Ping
		defer ticker.Stop()
		for range ticker.C {
			if err := conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				log.Printf("Failed to send ping to client %s: %v", logFilePath, err)
				return
			}
		}
	}()
	// --- 心跳機制結束 ---


	// 可選：將日誌檔案的現有內容發送給新的客戶端
	if content, err := os.ReadFile(logFilePath); err == nil {
		if len(content) > 0 {
			if err := conn.WriteMessage(websocket.TextMessage, content); err != nil {
				log.Printf("Failed to send initial log content %s: %v", logFilePath, err)
			}
		}
	} else {
		log.Printf("Failed to read initial log content %s: %v", logFilePath, err)
	}

	// 初始化 tail 追蹤器
	t, err := tail.TailFile(logFilePath, tail.Config{
		Follow:    true,
		ReOpen:    true,
		Poll:      true,
		MustExist: false,
	})
	if err != nil {
		log.Printf("Failed to tail log file %s: %v", logFilePath, err)
		return
	}
	defer t.Cleanup()

	// 監聽 tail 庫的 Lines 通道
	for {
		select {
		case line, ok := <-t.Lines:
			if !ok {
				log.Printf("Tail read channel closed, log file might be deleted or encountered serious issue %s", logFilePath)
				return
			}
			if line.Err != nil {
				log.Printf("Tail monitoring error for %s: %v", logFilePath, line.Err)
			}

			if err := conn.WriteMessage(websocket.TextMessage, []byte(line.Text+"\n")); err != nil {
				log.Printf("WebSocket write error for %s: %v", logFilePath, err)
				return
			}
		case <-r.Context().Done():
			log.Printf("Client disconnected from %s WebSocket (Context Done)", logFilePath)
			return
		}
	}
}

func main() {
	// 設置 HTTP 服務，將 /ws/uwsgi_log 路徑映射到 wsHandler
	http.HandleFunc("/ws/uwsgi_log", wsHandler)

	// ====== 這是為了解決直接訪問 /logs/ (對應 Go 應用程式的 /) 404 問題的最小改動 ======
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 檢查確保確實是根路徑的請求，避免意外處理其他路徑
		if r.URL.Path != "/" {
			http.NotFound(w, r) // 如果不是根路徑，仍然返回 404
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Log viewer backend is running. Connect to /ws/uwsgi_log via WebSocket for logs."))
	})
	// ====================================================================================

	// 啟動 HTTP 服務
	port := ":1111" // Go 應用程式監聽的端口
	log.Printf("Go Log Viewer Server listening on %s for uWSGI log", port)
	err := http.ListenAndServe(port, nil)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
