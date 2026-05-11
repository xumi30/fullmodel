// Command voice_dialog_mac records from the default macOS microphone via FFmpeg (avfoundation),
// sends one utterance over fullmodel WebSocket /v1/voice/dialog/stream, and saves the reply PCM.
//
// Prereqs (macOS):
//
//	brew install ffmpeg
//
// FFmpeg **仅录麦克风** 时，`-i` 必须是 **`none:<音频索引>`**（不能只写 `:1`，否则会报 Invalid audio device index）。
//
//	  ffmpeg -f avfoundation -list_devices true -i ""
//
// 列完设备后 ffmpeg **常会非 0 退出**（例如 251），仍可照常读出上面的设备表；也可用本程序的 `-list-devices-only`。
//
// 在输出里找到 `[AVFoundation audio devices]`，把 **第一个内置麦克风** 的索引设为 `none:0`；不对就试 `none:1`、`none:2`… 或用本程序的 `-list-devices-only`。
//
// Terminal A:
//
//	export DASHSCOPE_API_KEY=...
//	go run ./cmd/fullmodel serve -addr 127.0.0.1:8080
//
// Terminal B (no auth on serve):
//
//	go run ./examples/voice_dialog_mac -seconds 5
//
// With HTTP API key on serve, pass the same key:
//
//	go run ./examples/voice_dialog_mac -apikey "$FULLMODEL_API_KEY"
//
// Playback (match serve default TTS sample_rate=24000, mono s16le):
//
//	ffplay -f s16le -ar 24000 -ch_layout mono reply.pcm
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// normalizeAVFoundationInput turns legacy ":N" into FFmpeg's audio-only form "none:N".
// See https://trac.ffmpeg.org/wiki/Capture/Desktop#macOS
func normalizeAVFoundationInput(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "none:0"
	}
	if spec == ":" {
		return "none:0"
	}
	if strings.HasPrefix(spec, ":") && !strings.Contains(spec[1:], ":") {
		return "none:" + strings.TrimPrefix(spec, ":")
	}
	return spec
}

// Lines look like:
//
//	[AVFoundation indev @ 0x...] [0] MacBook Air Microphone
//
// (must only scan the substring after "AVFoundation audio devices:".)
var avfoundationAudioCaptureLine = regexp.MustCompile(`\]\s*\[(\d+)\]\s*(.+)`)

func ffmpegAVFoundationCombinedOutput(ffmpegBin string, args ...string) ([]byte, error) {
	cmd := exec.Command(ffmpegBin, args...)
	return cmd.CombinedOutput()
}

// firstSuggestedAVFoundationMic returns "none:N" from the listing after the audio-devices banner.
func firstSuggestedAVFoundationMic(listing string) (spec string, deviceName string) {
	const banner = "AVFoundation audio devices:"
	i := strings.Index(listing, banner)
	if i < 0 {
		return "", ""
	}
	tail := listing[i+len(banner):]
	if j := strings.Index(tail, "Error opening input"); j > 0 {
		tail = tail[:j]
	}
	for _, line := range strings.Split(tail, "\n") {
		if !strings.Contains(line, "AVFoundation indev") {
			continue
		}
		m := avfoundationAudioCaptureLine.FindStringSubmatch(line)
		if len(m) >= 3 {
			idxStr := strings.TrimSpace(m[1])
			if _, err := strconv.Atoi(idxStr); err != nil {
				continue
			}
			name := strings.TrimSpace(m[2])
			return "none:" + idxStr, name
		}
	}
	return "", ""
}

func logAVFoundationListing(execErr error, combined []byte, context string) {
	msg := truncateOut(string(combined), 12000)
	if strings.TrimSpace(msg) != "" {
		log.Printf("[dialog_mac] %s ffmpeg output (exec_err=%v):\n%s", context, execErr, msg)
	} else if execErr != nil {
		log.Printf("[dialog_mac] %s ffmpeg failed: %v", context, execErr)
		return
	}
	if strings.Contains(string(combined), "AVFoundation audio devices:") {
		if execErr != nil {
			log.Printf("[dialog_mac] %s note: exit after -list_devices is often non-zero (e.g. exit 251); device list above is still valid",
				context)
		}
		if spec, name := firstSuggestedAVFoundationMic(string(combined)); spec != "" {
			log.Printf("[dialog_mac] %s suggested mic: %q → run with -av-audio %s", context, name, spec)
		}
	}
}

func truncateOut(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n…(truncated)"
}

func redactWSURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	if q.Get("api_key") != "" {
		q.Set("api_key", "***")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func main() {
	if runtime.GOOS != "darwin" {
		log.Fatalf("this example targets macOS (uses ffmpeg -f avfoundation); GOOS=%s", runtime.GOOS)
	}

	wsBase := flag.String("url", "ws://127.0.0.1:8080/v1/voice/dialog/stream", "WebSocket URL")
	outPCM := flag.String("o", "reply.pcm", "write assistant TTS PCM here (s16le mono per server format=pcm)")
	seconds := flag.Float64("seconds", 5, "record this many seconds from the mic (FFmpeg -t)")
	ffmpegBin := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	avAudio := flag.String("av-audio", "none:0", `only-audio FFmpeg -i argument: use none:<idx> e.g. none:0; legacy ":N" is accepted as none:N`)
	listDevOnly := flag.Bool("list-devices-only", false, "print avfoundation capture devices via ffmpeg then exit")
	apiKey := flag.String("apikey", os.Getenv("FULLMODEL_API_KEY"), "optional; must match serve -api-key when auth is on")
	session := flag.String("session", "mac-demo", "chat session id for StreamChat memory")
	system := flag.String("system", "请用简洁、口语化的中文回答用户。", "optional system instruction (sent once in config)")
	voice := flag.String("voice", "Cherry", "TTS voice query param")
	verbose := flag.Bool("verbose", false, "log every binary chunk and JSON frame")
	flag.Parse()

	if *listDevOnly {
		log.Println("[dialog_mac] listing devices (ffmpeg -f avfoundation -list_devices true -i \"\")")
		combined, execErr := ffmpegAVFoundationCombinedOutput(*ffmpegBin,
			"-f", "avfoundation", "-list_devices", "true", "-i", "",
		)
		logAVFoundationListing(execErr, combined, "list_devices")
		return
	}

	avIn := normalizeAVFoundationInput(*avAudio)
	if avIn != strings.TrimSpace(*avAudio) {
		log.Printf("[dialog_mac] av-audio normalized %q -> %q", *avAudio, avIn)
	}

	u, err := url.Parse(*wsBase)
	if err != nil {
		log.Fatal(err)
	}
	q := u.Query()
	if k := strings.TrimSpace(*apiKey); k != "" {
		q.Set("api_key", k)
	}
	if v := strings.TrimSpace(*voice); v != "" {
		q.Set("voice", v)
	}
	u.RawQuery = q.Encode()

	hdr := http.Header{}
	if k := strings.TrimSpace(*apiKey); k != "" {
		hdr.Set("X-API-Key", k)
	}

	log.Printf("[dialog_mac] dial %s", redactWSURL(u.String()))
	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), hdr)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			log.Fatalf("dial: %v HTTP %s %s", err, resp.Status, strings.TrimSpace(string(b)))
		}
		log.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"op":         "config",
		"session_id": *session,
		"system":     *system,
		"voice":      *voice,
	}); err != nil {
		log.Fatal(err)
	}
	_, configBody, err := conn.ReadMessage()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[dialog_mac] config resp: %s", string(configBody))

	if err := conn.WriteJSON(map[string]any{"op": "ping"}); err != nil {
		log.Fatal(err)
	}
	_, pong, err := conn.ReadMessage()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[dialog_mac] ping resp: %s", string(pong))

	log.Printf("[dialog_mac] ffmpeg capture -i %q (mono 16kHz WAV to websocket)", avIn)
	cmd := exec.Command(*ffmpegBin,
		"-nostats", "-loglevel", "error",
		"-f", "avfoundation",
		"-i", avIn,
		"-t", fmt.Sprintf("%g", *seconds),
		"-ar", "16000",
		"-acodec", "pcm_s16le",
		"-ac", "1",
		"-f", "wav",
		"pipe:1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("ffmpeg start: %v (is ffmpeg installed? brew install ffmpeg)", err)
	}

	errCh := make(chan error, 1)
	pcmOut, err := os.Create(*outPCM)
	if err != nil {
		log.Fatal(err)
	}
	defer pcmOut.Close()

	go func() {
		errCh <- receiveLoop(conn, pcmOut, *verbose)
	}()

	r := bufio.NewReader(stdout)
	buf := make([]byte, 32*1024)
	sent := 0
	nframes := 0
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			nframes++
			sent += n
			if *verbose || nframes == 1 || nframes%20 == 0 {
				log.Printf("[dialog_mac] ws binary frame #%d bytes=%d total_sent=%d", nframes, n, sent)
			}
			if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
				log.Printf("[dialog_mac] ws write: %v", werr)
				break
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			log.Printf("[dialog_mac] ffmpeg read: %v", rerr)
			break
		}
	}
	waitErr := cmd.Wait()
	log.Printf("[dialog_mac] ffmpeg done total_wav_bytes=%d wait_err=%v", sent, waitErr)

	if sent == 0 {
		log.Println("[dialog_mac] ------------------------------------------------------------")
		log.Println("[dialog_mac] captured 0 bytes — microphone input did not open.")
		log.Println("[dialog_mac] Use: ffmpeg -f avfoundation -list_devices true -i \"\"")
		log.Println("[dialog_mac] then pass -av-audio none:<AUDIO_INDEX>  (examples: none:0, none:1)")
		combined, listErr := ffmpegAVFoundationCombinedOutput(*ffmpegBin,
			"-f", "avfoundation", "-list_devices", "true", "-i", "",
		)
		logAVFoundationListing(listErr, combined, "zero_audio_hint")
		os.Exit(1)
	}

	if err := conn.WriteJSON(map[string]any{"op": "end_turn"}); err != nil {
		log.Fatal(err)
	}
	log.Printf("[dialog_mac] sent end_turn")

	select {
	case rerr := <-errCh:
		if rerr != nil {
			log.Fatalf("receive: %v", rerr)
		}
	case <-time.After(5 * time.Minute):
		log.Fatal("timed out waiting for assistant reply")
	}

	log.Printf("[dialog_mac] done. Play: ffplay -f s16le -ar 24000 -ch_layout mono %s", *outPCM)
}

func receiveLoop(conn *websocket.Conn, pcmOut io.Writer, verbose bool) error {
	gotAssistant := false
	pcmChunks := int64(0)
	pcmBytes := int64(0)
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			if !gotAssistant {
				return fmt.Errorf("websocket read before assistant: %w", err)
			}
			return err
		}
		switch mt {
		case websocket.BinaryMessage:
			pcmChunks++
			pcmBytes += int64(len(data))
			if _, werr := pcmOut.Write(data); werr != nil {
				return werr
			}
			if verbose || pcmChunks == 1 || pcmChunks%50 == 0 {
				log.Printf("[dialog_mac] recv PCM chunk #%d (+ %d bytes, total pcm %d)",
					pcmChunks, len(data), pcmBytes)
			}
		case websocket.TextMessage:
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				log.Printf("[dialog_mac] recv non-json text: %s", string(data))
				continue
			}
			op, _ := m["op"].(string)
			switch op {
			case "asr":
				log.Printf("[dialog_mac] ASR: %#v", m["text"])
			case "assistant":
				gotAssistant = true
				log.Printf("[dialog_mac] assistant: %#v", m["text"])
				return nil
			case "done":
				log.Printf("[dialog_mac] server done event")
			case "event":
				log.Printf("[dialog_mac] upstream event type=%v", m["type"])
			case "error":
				return fmt.Errorf("server error: %v", m["message"])
			default:
				if verbose {
					log.Printf("[dialog_mac] json %+v", m)
				}
			}
		}
	}
}
