package brain

import (
	"context"
	"time"
)

// 流式输出接口
type StreamOutput interface {
	// 返回文本流
	Text() <-chan string

	// 返回错误流
	Error() <-chan error

	// 返回工具调用流
	ToolCalls() <-chan []ToolCall

	// 取消流处理
	Cancel()

	// 等待完成
	Wait() error
}

// 流式输出实现
type streamOutput struct {
	ctx      context.Context
	cancel   context.CancelFunc
	textCh   chan string
	errCh    chan error
	toolCh   chan []ToolCall
	done     chan struct{}
	finalErr error
	metrics  *StreamMetrics
}

// StreamMetrics 流处理指标
type StreamMetrics struct {
	StartTime         time.Time     // 流开始时间
	EndTime           time.Time     // 流结束时间
	TokenCount        int           // 生成的token总数
	FirstTokenLatency time.Duration // 首字延迟
}

func newStreamOutput(ctx context.Context, bufferSize int) *streamOutput {
	childCtx, cancel := context.WithCancel(ctx)
	return &streamOutput{
		ctx:     childCtx,
		cancel:  cancel,
		textCh:  make(chan string, bufferSize),
		errCh:   make(chan error, 1),
		toolCh:  make(chan []ToolCall, bufferSize),
		done:    make(chan struct{}),
		metrics: &StreamMetrics{StartTime: time.Now()},
	}
}

func (s *streamOutput) Text() <-chan string {
	return s.textCh
}

func (s *streamOutput) Error() <-chan error {
	return s.errCh
}

func (s *streamOutput) ToolCalls() <-chan []ToolCall {
	return s.toolCh
}

func (s *streamOutput) Cancel() {
	s.cancel()
}

func (s *streamOutput) Wait() error {
	<-s.done
	return s.finalErr
}

func (s *streamOutput) fail(err error) {
	if err == nil {
		return
	}
	s.finalErr = err
	select {
	case s.errCh <- err:
	case <-s.ctx.Done():
	}
}

// 完成流处理
func (s *streamOutput) complete(err error) {
	if err != nil || s.finalErr == nil {
		s.finalErr = err
	}
	close(s.textCh)
	close(s.errCh)
	close(s.toolCh)
	close(s.done)
	s.metrics.EndTime = time.Now()
}
