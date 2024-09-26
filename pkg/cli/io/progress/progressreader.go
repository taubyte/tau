package progress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"time"
)

type Reader struct {
	reader       io.Reader
	totalSize    int
	ch           chan int
	bytesRead    int
	lastPos      int
	lastReport   time.Time
	interval     time.Duration
	ctx          context.Context
	reportAsPerc bool
}

type Option func(*Reader) error

func WithBuffer(buf []byte) Option {
	return func(pr *Reader) error {
		if buf == nil {
			return errors.New("buffer cannot be nil")
		}
		pr.reader = bytes.NewReader(buf)
		pr.totalSize = len(buf)
		return nil
	}
}

func WithReader(r io.Reader, length int) Option {
	return func(pr *Reader) error {
		if r == nil {
			return errors.New("reader cannot be nil")
		}
		if length <= 0 {
			return errors.New("length must be greater than 0")
		}
		pr.reader = r
		pr.totalSize = length
		return nil
	}
}

func WithContext(ctx context.Context) Option {
	return func(pr *Reader) error {
		if ctx == nil {
			return errors.New("context cannot be nil")
		}
		pr.ctx = ctx
		return nil
	}
}

func Percentage() Option {
	return func(pr *Reader) error {
		pr.reportAsPerc = true
		return nil
	}
}

func New(interval time.Duration, opts ...Option) (*Reader, error) {
	pr := &Reader{
		ch:         make(chan int, 64),
		interval:   interval,
		lastReport: time.Now(),
	}

	for _, opt := range opts {
		if err := opt(pr); err != nil {
			return nil, err
		}
	}

	if pr.reader == nil {
		return nil, errors.New("a buffer or reader must be provided")
	}

	return pr, nil
}

func (pr *Reader) sendProgress(p int) {
	select {
	case pr.ch <- p:
	default:
	}
}

func (pr *Reader) Close() {
	if pr.ch != nil {
		close(pr.ch)
		pr.ch = nil
	}
}

func (pr *Reader) Read(p []byte) (n int, err error) {
	if pr.ch == nil {
		return 0, io.ErrUnexpectedEOF
	}

	if pr.ctx != nil {
		select {
		case <-pr.ctx.Done():
			pr.Close()
			return 0, pr.ctx.Err()
		default:
		}
	}

	n, err = pr.reader.Read(p)
	pr.bytesRead += n

	now := time.Now()
	if pr.bytesRead != pr.lastPos && now.Sub(pr.lastReport) >= pr.interval {
		if pr.reportAsPerc {
			percent := int(math.Ceil(float64(pr.bytesRead) / float64(pr.totalSize) * 100))
			pr.sendProgress(percent)
		} else {
			pr.sendProgress(pr.bytesRead)
		}
		pr.lastPos = pr.bytesRead
		pr.lastReport = now
	}

	if err != nil {
		if err == io.EOF {
			if pr.reportAsPerc {
				pr.sendProgress(100)
			} else {
				pr.sendProgress(pr.bytesRead)
			}
		}
		pr.Close()
	}

	return
}

func (pr *Reader) ProgressChan() <-chan int {
	return pr.ch
}
