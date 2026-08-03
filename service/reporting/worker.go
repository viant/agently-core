package reporting

import (
	"context"
	"fmt"
	"log"
	"time"
)

const (
	DefaultWorkerBatchLimit  = 1
	DefaultStaleRunningAfter = 15 * time.Minute
)

type WorkerOptions struct {
	Interval                     time.Duration
	BatchLimit                   int
	Logger                       func(format string, args ...interface{})
	ReconcileStaleRunningExports bool
	StaleRunningAfter            time.Duration
}

type Worker struct {
	service                      *Service
	interval                     time.Duration
	batchLimit                   int
	logger                       func(format string, args ...interface{})
	reconcileStaleRunningExports bool
	staleRunningAfter            time.Duration
}

func NewWorker(service *Service, options WorkerOptions) *Worker {
	batchLimit := options.BatchLimit
	if batchLimit < 1 {
		batchLimit = DefaultWorkerBatchLimit
	}
	loggerFn := options.Logger
	if loggerFn == nil {
		loggerFn = log.Printf
	}
	staleRunningAfter := options.StaleRunningAfter
	if staleRunningAfter <= 0 {
		staleRunningAfter = DefaultStaleRunningAfter
	}
	return &Worker{
		service:                      service,
		interval:                     options.Interval,
		batchLimit:                   batchLimit,
		logger:                       loggerFn,
		reconcileStaleRunningExports: options.ReconcileStaleRunningExports,
		staleRunningAfter:            staleRunningAfter,
	}
}

func (w *Worker) RunOnce(ctx context.Context) (*RunQueuedExportsResult, error) {
	if w == nil || w.service == nil {
		return nil, fmt.Errorf("reporting worker: service is required")
	}
	if w.batchLimit < 1 {
		return nil, fmt.Errorf("reporting worker: batch limit must be >= 1")
	}
	if w.reconcileStaleRunningExports {
		if _, err := w.service.ReconcileStaleRunningExports(ctx, w.staleRunningAfter); err != nil {
			return nil, err
		}
	}
	return w.service.RunQueuedExports(ctx, w.batchLimit)
}

func (w *Worker) Start(ctx context.Context) error {
	if w == nil || w.service == nil {
		return nil
	}
	if w.interval <= 0 {
		return fmt.Errorf("reporting worker: interval must be > 0")
	}
	if w.service.exporter == nil {
		return fmt.Errorf("reporting worker: exporter is not configured")
	}
	ticker := time.NewTicker(w.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := w.RunOnce(ctx); err != nil && w.logger != nil {
					w.logger("reporting worker: %v", err)
				}
			}
		}
	}()
	return nil
}
