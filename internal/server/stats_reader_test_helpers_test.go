// stats_reader_test_helpers_test.go 提供 chatStatsReader 的测试专用读取/解析入口。
//
// 生产路径只有一条解析器（internal/upstream 的统一事件管线，经
// WithFrameObserver → chatStatsReader.Observe）。本文件仅让旧观测测试能在不复制
// 生产解析逻辑的前提下逐行喂帧，避免第二套 SSE 解析进入生产代码。
package server

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// testStatsReader 测试专用包装：底层字节原样透出，同时逐行喂给生产观测方法。
type testStatsReader struct {
	*chatStatsReader
	br   *bufio.Reader
	pend []byte
}

func newChatStatsReaderSince(r io.Reader, since time.Time) *testStatsReader {
	return &testStatsReader{chatStatsReader: &chatStatsReader{start: since}, br: bufio.NewReaderSize(r, 64*1024)}
}

func (s *testStatsReader) Read(p []byte) (int, error) {
	if len(s.pend) > 0 {
		n := copy(p, s.pend)
		s.pend = s.pend[n:]
		return n, nil
	}
	line, err := s.br.ReadString('\n')
	if line != "" {
		s.parseSSELine(line)
		s.pend = []byte(line)
		n := copy(p, s.pend)
		s.pend = s.pend[n:]
		return n, nil
	}
	return 0, err
}

// parseSSELine 测试入口：把一行 SSE 拆成 JSON 后交给生产 Observe（不重复实现解析规则）。
func (s *chatStatsReader) parseSSELine(line string) {
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	payload := strings.TrimPrefix(line, "data: ")
	if payload == "[DONE]" {
		return
	}
	var obj map[string]any
	if json.Unmarshal([]byte(payload), &obj) != nil {
		return
	}
	s.Observe(obj)
}
