package dispatcher

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
)

type job struct {
	msg        imap.Message
	maxRetries int
	resCh      chan result
}

type result struct {
	analysis provider.AnalysisResponse
	err      error
}

type Dispatcher struct {
	providerType   string
	providerConfig map[string]string
	jobCh          chan *job
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	tokens         chan struct{}
}

// New creates and starts a dispatcher with the requested concurrency and
// optional rateLimit (calls per second). Pass a shutdownCtx that will be
// respected by the internal workers.
func New(shutdownCtx context.Context, providerType string, providerConfig map[string]string, concurrency int, rateLimit float64) (*Dispatcher, error) {
	if concurrency < 1 {
		return nil, errors.New("concurrency must be >= 1")
	}

	dctx, cancel := context.WithCancel(shutdownCtx)
	d := &Dispatcher{
		providerType:   providerType,
		providerConfig: providerConfig,
		jobCh:          make(chan *job, concurrency*4),
		cancel:         cancel,
	}

	// Rate limiter: produce tokens at rateLimit per second.
	if rateLimit > 0 {
		d.tokens = make(chan struct{}, int(math.Max(1, float64(concurrency))))
		tickDur := time.Duration(float64(time.Second) / rateLimit)
		if tickDur <= 0 {
			tickDur = time.Nanosecond
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			ticker := time.NewTicker(tickDur)
			defer ticker.Stop()
			for {
				select {
				case <-dctx.Done():
					return
				case <-ticker.C:
					select {
					case d.tokens <- struct{}{}:
					default:
					}
				}
			}
		}()
	}

	// Spawn worker goroutines.
	for i := 0; i < concurrency; i++ {
		d.wg.Add(1)
		go func(workerID int) {
			defer d.wg.Done()

			p, err := provider.New(providerType)
			if err != nil {
				logx.Errorf("dispatcher worker: unknown provider %s: %v", providerType, err)
				return
			}
			if err = p.Init(providerConfig); err != nil {
				logx.Errorf("dispatcher worker: could not init provider %s: %v", providerType, err)
				return
			}

			for {
				select {
				case <-dctx.Done():
					return
				case job := <-d.jobCh:
					// enforce rate limit per provider if enabled
					if d.tokens != nil {
						select {
						case <-dctx.Done():
							return
						case <-d.tokens:
						}
					}

					backoff := 1 * time.Second
					var res provider.AnalysisResponse
					var err error
					for attempt := 0; attempt <= job.maxRetries; attempt++ {
						res, err = p.Analyze(job.msg)
						if err == nil {
							break
						}
						if provider.IsNonRetryable(err) {
							break
						}
						if attempt < job.maxRetries {
							select {
							case <-dctx.Done():
								err = dctx.Err()
								break
							case <-time.After(backoff):
							}
							backoff = backoff * 2
							if backoff > 5*time.Minute {
								backoff = 5 * time.Minute
							}
						}
					}
					select {
					case job.resCh <- result{analysis: res, err: err}:
					case <-dctx.Done():
					}
				}
			}
		}(i)
	}

	return d, nil
}

// Analyze submits a message for analysis and blocks until a result or ctx
// cancellation. The call will wait until a worker accepts the job.
func (d *Dispatcher) Analyze(ctx context.Context, msg imap.Message, maxRetries int) (provider.AnalysisResponse, error) {
	resCh := make(chan result, 1)
	j := &job{msg: msg, maxRetries: maxRetries, resCh: resCh}
	select {
	case <-ctx.Done():
		return provider.AnalysisResponse{}, ctx.Err()
	case d.jobCh <- j:
	}
	select {
	case <-ctx.Done():
		return provider.AnalysisResponse{}, ctx.Err()
	case res := <-resCh:
		return res.analysis, res.err
	}
}

// Close stops the dispatcher workers and waits for them to exit.
func (d *Dispatcher) Close() {
	d.cancel()
	d.wg.Wait()
}
