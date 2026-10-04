package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"orkome/internal/contract/modelmeta"
)

const (
	modelMaxRetries   = 3
	modelMaxFailovers = 2
)

const retryBaseDelay = 200 * time.Millisecond

type retryClass int

const (
	classTransient retryClass = iota
	classAuth
	classRequest
)

func classifyModelErr(err error) retryClass {
	if err == nil {
		return classRequest
	}

	var apiErr *einoopenai.APIError
	if errors.As(err, &apiErr) {
		switch code := apiErr.HTTPStatusCode; {
		case code == 429, code == 408:
			return classTransient
		case code >= 500 && code < 600:
			return classTransient
		case code == 401, code == 403:
			return classAuth
		default:
			return classRequest
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case containsAny(msg,
		"rate limit", "too many requests", "429", "overload", "quota",
		"bad gateway", "service unavailable", "502", "503", "504",
		"timeout", "deadline exceeded", "connection refused", "connection reset",
		"broken pipe", "dial tcp", "no such host", "eof",
		"temporarily unavailable", "try again"):
		return classTransient
	case containsAny(msg, "unauthorized", "forbidden", "api key", "invalid_api_key", "authentication"):
		return classAuth
	case containsAny(msg, "context length", "token limit", "maximum context", "context window",
		"invalid request", "bad request", "not found", "model_not_found"):
		return classRequest
	}
	return classRequest
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func retryTransient(err error) bool { return classifyModelErr(err) == classTransient }

func failoverEligible(err error) bool {
	c := classifyModelErr(err)
	return c == classTransient || c == classAuth
}

func unwrapRetryExhausted(err error) error {
	var re *adk.RetryExhaustedError
	if errors.As(err, &re) && re.LastErr != nil {
		return re.LastErr
	}
	return err
}

type modelRetryStats struct {
	retries   atomic.Int32
	failovers atomic.Int32
}

func (s *modelRetryStats) report() (retries, failovers int) {
	retries, failovers = int(s.retries.Load()), int(s.failovers.Load())
	observeAbsorbed(retries, failovers)
	return retries, failovers
}

func backoffFn(_ context.Context, attempt int) time.Duration {
	if attempt < 1 {
		return retryBaseDelay
	}
	d := retryBaseDelay << (attempt - 1)
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	return d
}

func newModelRetryConfig(stats *modelRetryStats, backoff func(ctx context.Context, attempt int) time.Duration) *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries: modelMaxRetries,
		ShouldRetry: func(ctx context.Context, rc *adk.RetryContext) *adk.RetryDecision {
			if rc.Err == nil {
				return nil
			}
			if !retryTransient(rc.Err) {
				return nil
			}

			if rc.RetryAttempt <= modelMaxRetries {
				stats.retries.Add(1)
			}
			return &adk.RetryDecision{Retry: true}
		},
		BackoffFunc: backoff,
	}
}

type failoverChainFn func(ctx context.Context) ([]string, error)

func newModelFailoverConfig(ctx context.Context, rc modelmeta.RuntimeConfig, chain failoverChainFn, stats *modelRetryStats) *adk.ModelFailoverConfig[*schema.Message] {
	var once sync.Once
	var secrets []string
	resolve := func() {
		once.Do(func() {
			if chain == nil {
				return
			}
			secrets, _ = chain(ctx)
			if len(secrets) > modelMaxFailovers {
				secrets = secrets[:modelMaxFailovers]
			}
		})
	}
	return &adk.ModelFailoverConfig[*schema.Message]{
		MaxRetries: modelMaxFailovers,
		ShouldFailover: func(_ context.Context, _ *schema.Message, outputErr error) bool {
			if outputErr == nil {
				return false
			}
			return failoverEligible(unwrapRetryExhausted(outputErr))
		},
		GetFailoverModel: func(ctx context.Context, fc *adk.FailoverContext[*schema.Message]) (model.BaseModel[*schema.Message], []*schema.Message, error) {
			resolve()
			if fc.FailoverAttempt > uint(len(secrets)) {

				return nil, nil, unwrapRetryExhausted(fc.LastErr)
			}
			m, err := modelmeta.Build(ctx, rc, secrets[fc.FailoverAttempt-1])
			if err != nil {
				return nil, nil, err
			}
			stats.failovers.Add(1)
			return m, nil, nil
		},
	}
}

type auxRetryModel struct {
	inner   model.BaseChatModel
	stats   *modelRetryStats
	backoff func(ctx context.Context, attempt int) time.Duration
	sleep   func(ctx context.Context, d time.Duration) error
}

func newAuxRetryModel(inner model.BaseChatModel, stats *modelRetryStats) *auxRetryModel {
	return &auxRetryModel{inner: inner, stats: stats, backoff: backoffFn,
		sleep: func(ctx context.Context, d time.Duration) error {
			if d <= 0 {
				return nil
			}
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}}
}

func (a *auxRetryModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var lastErr error
	for attempt := 0; attempt <= modelMaxRetries; attempt++ {
		msg, err := a.inner.Generate(ctx, input, opts...)
		if err == nil {
			return msg, nil
		}
		if !retryTransient(err) {
			return nil, err
		}
		lastErr = err
		if attempt < modelMaxRetries {
			a.stats.retries.Add(1)
			if serr := a.sleep(ctx, a.backoff(ctx, attempt+1)); serr != nil {
				return nil, serr
			}
		}
	}
	return nil, lastErr
}

func (a *auxRetryModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return a.inner.Stream(ctx, input, opts...)
}
