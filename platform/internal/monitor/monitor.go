package monitor

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Pool Metrics
var (
	PoolIdleCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform",
		Subsystem: "pool",
		Name:      "idle_count",
		Help:      "Current number of idle containers in the pool",
	})

	PoolAcquisitionLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "agent_platform",
		Subsystem: "pool",
		Name:      "acquisition_latency_seconds",
		Help:      "Latency of acquiring a container from the pool",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	})

	ContainerCreationErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "agent_platform",
		Subsystem: "pool",
		Name:      "container_creation_errors_total",
		Help:      "Total number of container creation errors",
	})

	PoolManagedCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform",
		Subsystem: "pool",
		Name:      "managed_count",
		Help:      "Total number of containers managed by the pool (idle + leased)",
	})
)

// Dispatcher Metrics
var (
	DispatcherActiveStreams = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform",
		Subsystem: "dispatcher",
		Name:      "active_streams",
		Help:      "Number of currently active gRPC streams",
	})

	DispatcherRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "agent_platform",
		Subsystem: "dispatcher",
		Name:      "requests_total",
		Help:      "Total number of dispatch requests",
	})

	DispatcherErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "agent_platform",
		Subsystem: "dispatcher",
		Name:      "errors_total",
		Help:      "Total number of dispatch errors",
	})
)

// Session Metrics
var (
	SessionActiveCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform",
		Subsystem: "session",
		Name:      "active_count",
		Help:      "Number of currently active sessions",
	})

	SessionCreationLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "agent_platform",
		Subsystem: "session",
		Name:      "creation_latency_seconds",
		Help:      "Latency of creating a new session",
		Buckets:   []float64{0.1, 0.5, 1, 2.5, 5, 10, 30},
	})
)
