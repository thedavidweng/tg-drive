package commands

import (
	"bufio"
	"context"
	"io"
	"os"
	"sync"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

type stdinLine struct {
	text string
	err  error
}

var (
	stdinOnce  sync.Once
	stdinLines chan stdinLine
)

// readLine reads one line of stdin for a prompt, or returns ERR_CANCELLED
// once ctx is cancelled. A blocked terminal read cannot be interrupted, so
// one goroutine owns stdin and every prompt reads through it; that keeps
// lines buffered ahead of one prompt from being lost to the next. Like
// bufio.Reader.ReadString, a final unterminated line comes with its error.
func readLine(ctx context.Context) (string, error) {
	stdinOnce.Do(func() {
		stdinLines = make(chan stdinLine)
		go func() {
			r := bufio.NewReader(os.Stdin)
			for {
				s, err := r.ReadString('\n')
				stdinLines <- stdinLine{text: s, err: err}
				if err != nil {
					close(stdinLines)
					return
				}
			}
		}()
	})
	select {
	case l, ok := <-stdinLines:
		if !ok {
			return "", io.EOF
		}
		return l.text, l.err
	case <-ctx.Done():
		return "", apperr.Cancelled()
	}
}
