// Command voice_analyze_http_client calls fullmodel POST /v1/voice/analyze-tags (multipart).
//
// Terminal A:
//
//	go run ./cmd/fullmodel serve -addr 127.0.0.1:8080
//
// Terminal B（可用真实 mp3/wav，或用 -synthetic 生成一段测试 WAV）:
//
//	go run ./examples/voice_analyze_http_client -url "http://127.0.0.1:8080/v1/voice/analyze-tags" -audio ./sample.mp3
//	go run ./examples/voice_analyze_http_client -synthetic
//
// 若 serve 启用了 HTTP 鉴权：
//
//	go run ./examples/voice_analyze_http_client -synthetic -apikey "$FULLMODEL_API_KEY"
//
// 日志对齐：客户端打印 clientTrace；服务端日志搜 trace= 与响应头 X-Fullmodel-Trace-Id。
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	baseURL := flag.String("url", "http://127.0.0.1:8080/v1/voice/analyze-tags", "POST URL for /v1/voice/analyze-tags")
	audioPath := flag.String("audio", "", "audio file path (or use -synthetic)")
	synthetic := flag.Bool("synthetic", false, "write a short synthetic WAV to a temp file and analyze it")
	apiKey := flag.String("apikey", os.Getenv("FULLMODEL_API_KEY"), "optional X-API-Key when serve auth is on")
	verbose := flag.Bool("verbose", true, "log trace and timing")
	flag.Parse()

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	clientTrace := "cli-" + hex.EncodeToString(b)
	var audioData []byte
	var fname string

	if *synthetic {
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("fullmodel_analyze_test_%s.wav", clientTrace[:8]))
		if err := writeSyntheticWAV(tmp, 16000, 16000*12); err != nil {
			log.Fatal(err)
		}
		defer os.Remove(tmp)
		var err error
		audioData, err = os.ReadFile(tmp)
		if err != nil {
			log.Fatal(err)
		}
		fname = "synthetic-12s.wav"
		if *verbose {
			log.Printf("[voice_analyze_client] synthetic wav bytes=%d path=%s", len(audioData), tmp)
		}
	} else {
		if strings.TrimSpace(*audioPath) == "" {
			flag.Usage()
			log.Fatal("need -audio or -synthetic")
		}
		var err error
		audioData, err = os.ReadFile(*audioPath)
		if err != nil {
			log.Fatal(err)
		}
		fname = filepath.Base(*audioPath)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("audio", fname)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := part.Write(audioData); err != nil {
		log.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		log.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, *baseURL, &buf)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Client-Trace-Id", clientTrace)
	if k := strings.TrimSpace(*apiKey); k != "" {
		req.Header.Set("X-API-Key", k)
	}

	if *verbose {
		log.Printf("[voice_analyze_client] begin client_trace=%s POST %s audio_bytes=%d file=%q",
			clientTrace, *baseURL, len(audioData), fname)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			log.Fatalf("request: %v — is fullmodel serve running?", err)
		}
		log.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start).Truncate(time.Millisecond)

	serverTrace := resp.Header.Get("X-Fullmodel-Trace-Id")
	if *verbose {
		snip := string(body)
		if len(snip) > 4000 {
			snip = snip[:4000] + "…(truncated)"
		}
		log.Printf("[voice_analyze_client] response http=%s elapsed=%s X-Fullmodel-Trace-Id=%q body_len=%d",
			resp.Status, elapsed, serverTrace, len(body))
		log.Printf("[voice_analyze_client] raw_body: %s", snip)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Fatalf("HTTP %s (client_trace=%s server_trace=%q): %s", resp.Status, clientTrace, serverTrace, strings.TrimSpace(string(body)))
	}

	var env struct {
		ID     string `json:"id"`
		Status struct {
			Success bool   `json:"success"`
			Error   string `json:"error,omitempty"`
		} `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		log.Fatalf("parse envelope: %v body=%s", err, string(body))
	}
	if *verbose {
		log.Printf("[voice_analyze_client] envelope_id=%q client_trace=%s server_trace_hdr=%q match=%v",
			env.ID, clientTrace, serverTrace, serverTrace != "" && clientTrace == serverTrace)
	}
	if !env.Status.Success {
		log.Fatalf("brain status error: %s (trace server=%q client=%s)", env.Status.Error, serverTrace, clientTrace)
	}

	var pretty bytes.Buffer
	_ = json.Indent(&pretty, env.Result, "", "  ")
	fmt.Println(pretty.String())
	log.Printf("[voice_analyze_client] done client_trace=%s elapsed=%s", clientTrace, elapsed)
}

// writeSyntheticWAV writes PCM S16LE mono for smoke tests (non-silent samples).
func writeSyntheticWAV(path string, sampleRate, numSamples int) error {
	dataSize := numSamples * 2
	buf := make([]byte, 44+dataSize)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+dataSize))
	copy(buf[8:], "WAVE")
	copy(buf[12:], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)
	binary.LittleEndian.PutUint16(buf[22:], 1)
	binary.LittleEndian.PutUint32(buf[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(buf[32:], 2)
	binary.LittleEndian.PutUint16(buf[34:], 16)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], uint32(dataSize))
	for i := 0; i < numSamples; i++ {
		v := int16((i%1000)*8 - 4000)
		binary.LittleEndian.PutUint16(buf[44+i*2:], uint16(v))
	}
	return os.WriteFile(path, buf, 0o644)
}
