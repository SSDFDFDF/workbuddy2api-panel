// writedeadline.go 非流式响应的写超时包装。
//
// 为什么需要：http.Server 不设全局 WriteTimeout（长 SSE 合法时长可达数分钟，
// 全局值会误杀在途流），SSE 路径逐帧设置了写 deadline，但非流式响应（本地聚合
// 的完整 JSON）此前没有任何写上限：客户端保持连接却停止读取时，Write 会一直
// 阻塞在 TCP 发送缓冲上，handler 线程、响应内存与账号在途名额被长期占住
// （inference 的 ctx 超时只管上游阶段，不会自动变成连接写 deadline）。
//
// 设计：按块写入并在每块前重设 deadline——完全停滞的连接在 timeout 内被掐断，
// 而“慢但持续有进度”的客户端不受影响（每块都能在 timeout 内推完即可）。
package server

import (
	"io"
	"net/http"
	"time"
)

// nonStreamWriteTimeout 单次写入（每块）的停滞上限。取 30s：远大于正常的
// 本地回环/局域网写出，又足以让卡死客户端及时释放 handler 与租约。
const nonStreamWriteTimeout = 30 * time.Second

// writeChunk 分块粒度：256 KiB。既避免单次巨写把 deadline 变成“整包时限”，
// 也不会因过碎的分块增加 syscall 次数。
const writeChunk = 256 << 10

// writeDeadlineWriter 在写入前后设置连接写 deadline 的 ResponseWriter 包装。
// 只包装非流式响应路径；SSE 路径已有自己的逐帧 deadline。
type writeDeadlineWriter struct {
	w       http.ResponseWriter
	timeout time.Duration
	// err 记录首次写失败，供调用方在结束时判定“下游交付失败”（不再重试上游）。
	err error
}

func newWriteDeadlineWriter(w http.ResponseWriter, timeout time.Duration) *writeDeadlineWriter {
	if timeout <= 0 {
		timeout = nonStreamWriteTimeout
	}
	return &writeDeadlineWriter{w: w, timeout: timeout}
}

func (d *writeDeadlineWriter) Header() http.Header { return d.w.Header() }

func (d *writeDeadlineWriter) WriteHeader(code int) {
	d.touch()
	d.w.WriteHeader(code)
}

// Write 分块写出，每块前重设 deadline；返回累计写入字节数。
// 语义仍是 io.Writer：只有出错时才返回 n < len(p)。
func (d *writeDeadlineWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > writeChunk {
			n = writeChunk
		}
		d.touch()
		written, err := d.w.Write(p[:n])
		total += written
		if err != nil {
			d.err = err
			return total, err
		}
		if written < n {
			// 底层违反 Writer 约定（未返回 error 却少写）：按 io.ErrShortWrite 处理，
			// 否则调用方会误以为全部写出。
			d.err = io.ErrShortWrite
			return total, io.ErrShortWrite
		}
		p = p[n:]
	}
	return total, nil
}

// Unwrap 让 http.ResponseController 能沿链找到底层连接（保留 Flush/Hijack 等能力）。
func (d *writeDeadlineWriter) Unwrap() http.ResponseWriter { return d.w }

// Flush 透传（非流式路径不会用到；保留以防未来复用）。
func (d *writeDeadlineWriter) Flush() {
	if f, ok := d.w.(http.Flusher); ok {
		f.Flush()
	}
}

// touch 重设连接写 deadline。底层不支持 SetWriteDeadline（如部分测试替身）时忽略。
func (d *writeDeadlineWriter) touch() {
	_ = http.NewResponseController(d.w).SetWriteDeadline(time.Now().Add(d.timeout))
}
