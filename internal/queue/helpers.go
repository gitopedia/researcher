package queue

import "context"

// SubmitLLM submits a typed LLM call through the queue, blocking until the
// result is available. The closure fn will be retried automatically if the
// LLM service is unavailable.
func SubmitLLM[T any](ctx context.Context, q *Queue, label string, fn func(ctx context.Context) (T, error)) (T, error) {
	raw, err := q.Submit(ctx, JobTypeLLM, label, func(ctx context.Context) (interface{}, error) {
		return fn(ctx)
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return raw.(T), nil
}

// SubmitComfyUI submits a typed ComfyUI call through the queue, blocking
// until the result is available.
func SubmitComfyUI[T any](ctx context.Context, q *Queue, label string, fn func(ctx context.Context) (T, error)) (T, error) {
	raw, err := q.Submit(ctx, JobTypeComfyUI, label, func(ctx context.Context) (interface{}, error) {
		return fn(ctx)
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return raw.(T), nil
}
