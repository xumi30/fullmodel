// Command voice_tts_ws_client connects to fullmodel serve WebSocket TTS and saves PCM.
//
// Terminal A:
//
//	export DASHSCOPE_API_KEY=...   # 与 config/llm.yaml 中一致
//	go run ./cmd/fullmodel serve -addr 127.0.0.1:8080   # 端口需与 voice_tts_ws_client -url 一致
//
// 若 shell 里设置了 FULLMODEL_API_KEY，serve 会默认启用 HTTP 鉴权（见 cmd/fullmodel -api-key）。
// 此时客户端也必须带同一密钥，否则会握手失败 (websocket: bad handshake / HTTP 401)：
//
//	go run ./examples/voice_tts_ws_client -apikey "$FULLMODEL_API_KEY"
//
// 本地调试可不启用鉴权：启动 serve 前执行 unset FULLMODEL_API_KEY，或显式：
//
//	go run ./cmd/fullmodel serve -api-key ""
//
// Terminal B（无 HTTP 鉴权时）:
//
//	go run ./examples/voice_tts_ws_client -text "你好"
//
// 与 serve 端口一致（如 8060），并打印详细收发日志：
//
//	go run ./examples/voice_tts_ws_client -verbose \
//	  -url "ws://127.0.0.1:8060/v1/voice/tts/stream" \
//	  -text "你好，测试实时 TTS"
//
// 播放（FFmpeg 8+ ffplay 用 -ch_layout，勿用 -ac）:
//	ffplay -f s16le -ar 24000 -ch_layout mono tts_out.pcm
// 或: ffmpeg -f s16le -ar 24000 -ac 1 -i tts_out.pcm tts_out.wav && afplay tts_out.wav
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/websocket"
)

func main() {
	wsURL := flag.String("url", "ws://127.0.0.1:8080/v1/voice/tts/stream", "WebSocket URL (same path as GET in serve)")
	outPath := flag.String("o", "tts_out.pcm", "output PCM file")
	text := flag.String("text", "你好，这是 WebSocket 实时语音测试。", "text to synthesize")
	apiKey := flag.String("apikey", os.Getenv("FULLMODEL_API_KEY"), "optional; must match fullmodel serve -api-key when auth is enabled")
	verbose := flag.Bool("verbose", false, "log each WebSocket message and PCM chunk stats")
	flag.Parse()

	u, err := url.Parse(*wsURL)
	if err != nil {
		log.Fatal(err)
	}
	hdr := http.Header{}
	if k := strings.TrimSpace(*apiKey); k != "" {
		hdr.Set("X-API-Key", k)
		q := u.Query()
		q.Set("api_key", k)
		u.RawQuery = q.Encode()
	}

	if *verbose {
		log.Printf("[tts_client] dialing %s", u.String())
	}
	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), hdr)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			snip, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			msg := strings.TrimSpace(string(snip))
			if msg != "" {
				log.Fatalf("dial: %v (HTTP %s: %s)", err, resp.Status, msg)
			}
			log.Fatalf("dial: %v (HTTP %s)", err, resp.Status)
		}
		if strings.Contains(err.Error(), "connection refused") {
			log.Fatalf("dial: %v — nothing is listening on this host:port. Start serve with the same port, e.g. go run ./cmd/fullmodel serve -addr 127.0.0.1:8060", err)
		}
		log.Fatal("dial:", err)
	}
	defer conn.Close()
	if *verbose {
		log.Printf("[tts_client] connected upgrade_ok")
	}

	send := func(op, t string) error {
		m := map[string]string{"op": op}
		if t != "" {
			m["text"] = t
		}
		return conn.WriteJSON(m)
	}
	if *verbose {
		log.Printf("[tts_client] send append text_len=%d preview=%q", len([]rune(*text)), preview(*text, 64))
	}
	if err := send("append", *text); err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Println("[tts_client] send finish")
	}
	if err := send("finish", ""); err != nil {
		log.Fatal(err)
	}

	f, err := os.Create(*outPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			if *verbose {
				log.Printf("[tts_client] read ended: %v", err)
			}
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				log.Println("read:", err)
			}
			break
		}
		if mt == websocket.BinaryMessage {
			if *verbose {
				log.Printf("[tts_client] recv binary bytes=%d", len(data))
			}
			if _, err := f.Write(data); err != nil {
				log.Fatal(err)
			}
			continue
		}
		if *verbose {
			log.Printf("[tts_client] recv text bytes=%d raw=%s", len(data), string(data))
		}
		var env struct {
			Op      string `json:"op"`
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			fmt.Printf("text(ignored): %s\n", string(data))
			continue
		}
		switch env.Op {
		case "event":
			fmt.Printf("event: %s\n", env.Type)
		case "error":
			log.Fatalf("server error: %s", env.Message)
		case "done":
			fmt.Println("done")
			if *verbose {
				stat, _ := f.Stat()
				log.Printf("[tts_client] output file %s size=%d", *outPath, stat.Size())
			}
			return
		}
	}
}

func preview(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}
