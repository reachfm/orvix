package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Worker struct {
	service       *Service
	registry      *Registry
	id            string
	pollInterval  time.Duration
	leaseDuration time.Duration
	onError       func(error)
}

func (w *Worker) WithErrorHandler(handler func(error)) *Worker {
	w.onError = handler
	return w
}

func NewWorker(service *Service, registry *Registry, id string) *Worker {
	return &Worker{service: service, registry: registry, id: id, pollInterval: time.Second, leaseDuration: 30 * time.Second}
}

func (w *Worker) WithIntervals(poll, lease time.Duration) *Worker {
	if poll > 0 {
		w.pollInterval = poll
	}
	if lease > 0 {
		w.leaseDuration = lease
	}
	return w
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if _, err := w.service.RecoverExpired(ctx, 50); err != nil && !errors.Is(err, context.Canceled) {
			w.report(err)
		}
		worked, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			w.report(err)
			worked = false
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Worker) report(err error) {
	if w.onError != nil && err != nil {
		w.onError(err)
	}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.service.Claim(ctx, w.id, w.leaseDuration)
	if err != nil || job == nil {
		return false, err
	}
	definition, ok := w.registry.Lookup(job.Type)
	if !ok {
		return true, w.service.Fail(ctx, leaseFor(job), "JOB_TYPE_UNREGISTERED", "automation job type is not registered", false)
	}
	execution := &jobExecution{service: w.service, lease: leaseFor(job), leaseDuration: w.leaseDuration, tenantID: job.TenantID}
	executionCtx, cancel := context.WithTimeout(ctx, definition.Timeout)
	defer cancel()
	result, handlerErr := w.executeWithHeartbeat(executionCtx, cancel, definition, execution, job.Payload)
	if errors.Is(handlerErr, context.Canceled) && ctx.Err() != nil {
		return true, ctx.Err()
	}
	requested, cancelErr := execution.CancellationRequested(context.Background())
	if cancelErr == nil && requested {
		return true, w.service.FinishCancellation(context.Background(), execution.lease)
	}
	if handlerErr != nil {
		var failure *ExecutionError
		if errors.As(handlerErr, &failure) {
			err = w.service.Fail(context.Background(), execution.lease, failure.Code, failure.Message, failure.Retryable)
		} else {
			err = w.service.Fail(context.Background(), execution.lease, "JOB_EXECUTION_FAILED", "automation job execution failed", true)
		}
		if errors.Is(err, ErrCancellationAsked) {
			err = w.service.FinishCancellation(context.Background(), execution.lease)
		}
		return true, err
	}
	return true, w.service.Complete(context.Background(), execution.lease, result)
}

func (w *Worker) executeWithHeartbeat(ctx context.Context, cancel context.CancelFunc, definition Definition, execution *jobExecution, payload json.RawMessage) (json.RawMessage, error) {
	type outcome struct {
		result json.RawMessage
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := w.executeSafely(ctx, definition, execution, payload)
		done <- outcome{result: result, err: err}
	}()
	interval := w.leaseDuration / 3
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case value := <-done:
			return value.result, value.err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if err := execution.Heartbeat(ctx); err != nil {
				cancel()
				return nil, err
			}
		}
	}
}

func (w *Worker) executeSafely(ctx context.Context, definition Definition, execution Execution, payload json.RawMessage) (result json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &ExecutionError{Code: "JOB_HANDLER_PANIC", Message: "automation job handler panicked", Retryable: false}
		}
	}()
	return definition.Handle(ctx, execution, payload)
}

type ExecutionError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *ExecutionError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

type jobExecution struct {
	service       *Service
	lease         Lease
	leaseDuration time.Duration
	tenantID      uint
}

func (e *jobExecution) TenantID() uint { return e.tenantID }

func (e *jobExecution) Heartbeat(ctx context.Context) error {
	return e.service.Heartbeat(ctx, e.lease, e.leaseDuration)
}
func (e *jobExecution) SetProgress(ctx context.Context, progress int) error {
	return e.service.UpdateProgress(ctx, e.lease, progress)
}
func (e *jobExecution) CancellationRequested(ctx context.Context) (bool, error) {
	return e.service.CancellationRequested(ctx, e.lease)
}
