package server

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

// deadlineProbeWriter 记录 SetWriteDeadline 调用次数，并可模拟“每次只接受 N 字节”的
// 慢连接；底层 ResponseWriter 的能力经 Unwrap 链暴露给 http.ResponseController。
type deadlineProbeWriter struct {
	header    http.Header
	deadlines int
	written   int
	failAfter int // >0 时在第 failAfter 次写入返回错误
	calls     int
	sizes     []int
	status    int
}

func (w *deadlineProbeWriter) Header() http.Header { return w.header }

func (w *deadlineProbeWriter) WriteHeader(code int) { w.status = code }

func (w *deadlineProbeWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.failAfter > 0 && w.calls > w.failAfter {
		return 0, errors.New("connection reset by peer")
	}
	// net/http 的 ResponseWriter 恒整写或返回错误（io.Writer 约定）：这里如实返回
	// len(p)，并记录每次调用的长度用于断言分块。
	w.sizes = append(w.sizes, len(p))
	w.written += len(p)
	return len(p), nil
}

func (w *deadlineProbeWriter) SetWriteDeadline(time.Time) error {
	w.deadlines++
	return nil
}

// Unwrap 供 http.ResponseController 沿链找到 SetWriteDeadline。
func (w *deadlineProbeWriter) Unwrap() http.ResponseWriter { return w }

// TestWriteDeadlineWriterChunksAndRefreshes 守护分块写出与逐块刷新 deadline：
// 单次巨写会让 deadline 变成“整包时限”，慢但持续的客户端会被误杀；
// 分块后每块前重设，且总字节数不变。
func TestWriteDeadlineWriterChunksAndRefreshes(t *testing.T) {
	base := &deadlineProbeWriter{header: http.Header{}}
	d := newWriteDeadlineWriter(base, time.Second)
	payload := make([]byte, writeChunk*2+123)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	n, err := d.Write(payload)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(payload) || base.written != len(payload) {
		t.Fatalf("written=%d/%d, want %d", n, base.written, len(payload))
	}
	if base.deadlines != 3 || len(base.sizes) != 3 {
		t.Fatalf("deadline 重设次数=%d 写入次数=%d, want 3/3（每块一次）", base.deadlines, len(base.sizes))
	}
	for i, size := range base.sizes {
		if size > writeChunk {
			t.Fatalf("第 %d 块 %d 字节超过分块上限 %d", i, size, writeChunk)
		}
	}
}

// TestWriteDeadlineWriterWriteHeaderSetsDeadline 响应头写出同样可能阻塞，必须一并设限。
func TestWriteDeadlineWriterWriteHeaderSetsDeadline(t *testing.T) {
	base := &deadlineProbeWriter{header: http.Header{}}
	d := newWriteDeadlineWriter(base, time.Second)
	d.WriteHeader(http.StatusOK)
	if base.deadlines != 1 || base.status != http.StatusOK {
		t.Fatalf("deadlines=%d status=%d", base.deadlines, base.status)
	}
}

// TestWriteDeadlineWriterPropagatesError 写失败必须原样返回并记录，
// 供调用方判定“下游交付失败”（不得据此重放上游生成）。
func TestWriteDeadlineWriterPropagatesError(t *testing.T) {
	base := &deadlineProbeWriter{header: http.Header{}, failAfter: 1}
	d := newWriteDeadlineWriter(base, time.Second)
	// 需要至少两块才能触发第二块的失败：用 writeChunk+10。
	_, err := d.Write(make([]byte, writeChunk+10))
	if err == nil {
		t.Fatal("want error")
	}
	if d.err == nil {
		t.Fatal("首次写失败必须记录到 err 供上层判定")
	}
	if !errors.Is(err, d.err) {
		t.Fatalf("err mismatch: %v vs %v", err, d.err)
	}
}

// TestWriteDeadlineWriterShortWrite 底层未返回错误却少写：按 io.ErrShortWrite 处理，
// 不能让调用方误以为整包已交付。
func TestWriteDeadlineWriterShortWrite(t *testing.T) {
	base := &shortWriter{header: http.Header{}}
	d := newWriteDeadlineWriter(base, time.Second)
	_, err := d.Write(make([]byte, 10))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err=%v, want io.ErrShortWrite", err)
	}
}

type shortWriter struct{ header http.Header }

func (w *shortWriter) Header() http.Header         { return w.header }
func (w *shortWriter) WriteHeader(int)             {}
func (w *shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }
