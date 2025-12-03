// Package metrics provides Prometheus metrics for the PBFT consensus engine.
package metrics

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for PBFT.
type Metrics struct {
	mu sync.RWMutex

	// Consensus metrics
	consensusRoundsTotal   prometheus.Counter // 총 합의 라운드
	consensusDuration      prometheus.Histogram // 합의 소유 시간
	currentBlockHeight     prometheus.Gauge // 현재 블록 높이
	currentView            prometheus.Gauge // 현 뷰 번호

	// Message metrics
	messagesSentTotal     *prometheus.CounterVec // 타입별 전송 메시지 수
	messagesReceivedTotal *prometheus.CounterVec // 타입별 수신 메시지 수
	messageProcessingTime *prometheus.HistogramVec // 메시지 처리 시간

	// View change metrics
	viewChangesTotal prometheus.Counter // 뷰 변경 횟수

	// Block metrics
	blockExecutionTime prometheus.Histogram	// 블록 실행 시간
	transactionsTotal  prometheus.Counter // 총 트랜잭션 수
	tps                prometheus.Gauge // 초당 트랜잭션ㅁ

	// Internal tracking
	roundStartTimes map[uint64]time.Time
	txCount         int64
	lastTpsUpdate   time.Time
}

// NewMetrics creates a new Metrics instance and registers all metrics.
func NewMetrics(namespace string) *Metrics {
	m := &Metrics{
		roundStartTimes: make(map[uint64]time.Time),
		lastTpsUpdate:   time.Now(),
	}

	// Consensus metrics
	m.consensusRoundsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "consensus_rounds_total",
		Help:      "Total number of consensus rounds completed",
	})

	m.consensusDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "consensus_duration_seconds",
		Help:      "Duration of consensus rounds in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~32s
	})

	m.currentBlockHeight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "block_height",
		Help:      "Current block height",
	})

	m.currentView = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "current_view",
		Help:      "Current view number",
	})

	// Message metrics
	m.messagesSentTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "messages_sent_total",
		Help:      "Total number of messages sent by type",
	}, []string{"type"})

	m.messagesReceivedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "messages_received_total",
		Help:      "Total number of messages received by type",
	}, []string{"type"})

	m.messageProcessingTime = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "message_processing_seconds",
		Help:      "Time to process messages by type",
		Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 12), // 0.1ms to ~400ms
	}, []string{"type"})

	// View change metrics
	m.viewChangesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "view_changes_total",
		Help:      "Total number of view changes",
	})

	// Block metrics
	m.blockExecutionTime = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "block_execution_seconds",
		Help:      "Time to execute blocks in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~4s
	})

	m.transactionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "transactions_total",
		Help:      "Total number of transactions processed",
	})

	m.tps = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "tps",
		Help:      "Current transactions per second",
	})

	// Register all metrics
	prometheus.MustRegister(
		m.consensusRoundsTotal,
		m.consensusDuration,
		m.currentBlockHeight,
		m.currentView,
		m.messagesSentTotal,
		m.messagesReceivedTotal,
		m.messageProcessingTime,
		m.viewChangesTotal,
		m.blockExecutionTime,
		m.transactionsTotal,
		m.tps,
	)

	return m
}

// StartConsensusRound records the start of a consensus round.
func (m *Metrics) StartConsensusRound(seqNum uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roundStartTimes[seqNum] = time.Now()
}

// EndConsensusRound records the end of a consensus round.
func (m *Metrics) EndConsensusRound(seqNum uint64) {
	m.mu.Lock()
	startTime, exists := m.roundStartTimes[seqNum]
	if exists {
		delete(m.roundStartTimes, seqNum)
	}
	m.mu.Unlock()

	if exists {
		duration := time.Since(startTime).Seconds()
		m.consensusDuration.Observe(duration)
		m.consensusRoundsTotal.Inc()
	}
}

// SetBlockHeight sets the current block height.
func (m *Metrics) SetBlockHeight(height uint64) {
	m.currentBlockHeight.Set(float64(height))
}

// SetCurrentView sets the current view number.
func (m *Metrics) SetCurrentView(view uint64) {
	m.currentView.Set(float64(view))
}

// IncrementMessagesSent increments the messages sent counter.
func (m *Metrics) IncrementMessagesSent(msgType string) {
	m.messagesSentTotal.WithLabelValues(msgType).Inc()
}

// IncrementMessagesReceived increments the messages received counter.
func (m *Metrics) IncrementMessagesReceived(msgType string) {
	m.messagesReceivedTotal.WithLabelValues(msgType).Inc()
}

// RecordMessageProcessingTime records the time to process a message.
func (m *Metrics) RecordMessageProcessingTime(msgType string, duration time.Duration) {
	m.messageProcessingTime.WithLabelValues(msgType).Observe(duration.Seconds())
}

// IncrementViewChanges increments the view change counter.
func (m *Metrics) IncrementViewChanges() {
	m.viewChangesTotal.Inc()
}

// RecordBlockExecutionTime records the block execution time.
func (m *Metrics) RecordBlockExecutionTime(duration time.Duration) {
	m.blockExecutionTime.Observe(duration.Seconds())
}

// AddTransactions adds to the transaction counter and updates TPS.
func (m *Metrics) AddTransactions(count int) {
	m.transactionsTotal.Add(float64(count))

	m.mu.Lock()
	m.txCount += int64(count)
	elapsed := time.Since(m.lastTpsUpdate).Seconds()
	if elapsed >= 1.0 {
		tps := float64(m.txCount) / elapsed
		m.tps.Set(tps)
		m.txCount = 0
		m.lastTpsUpdate = time.Now()
	}
	m.mu.Unlock()
}

// Server provides 프로메테우스 매트릭을 위한 HTTP 서버를 제공
type Server struct {
	addr   string
	server *http.Server
}

// NewServer creates a new metrics HTTP server.
func NewServer(addr string) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	return &Server{
		addr: addr,
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// Start starts the metrics server.
func (s *Server) Start() error {
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()
	return nil
}

// Stop stops the metrics server.
func (s *Server) Stop() error {
	return s.server.Close()
}

// GetMetricsSnapshot returns a snapshot of current metrics values.
type MetricsSnapshot struct {
	BlockHeight      uint64  `json:"block_height"`
	CurrentView      uint64  `json:"current_view"`
	ConsensusRounds  float64 `json:"consensus_rounds"`
	ViewChanges      float64 `json:"view_changes"`
	TotalTxs         float64 `json:"total_transactions"`
	CurrentTPS       float64 `json:"current_tps"`
}

// NullMetrics is a no-op implementation of metrics for testing.
type NullMetrics struct{}

func (n *NullMetrics) StartConsensusRound(seqNum uint64)                            {}
func (n *NullMetrics) EndConsensusRound(seqNum uint64)                              {}
func (n *NullMetrics) SetBlockHeight(height uint64)                                 {}
func (n *NullMetrics) SetCurrentView(view uint64)                                   {}
func (n *NullMetrics) IncrementMessagesSent(msgType string)                         {}
func (n *NullMetrics) IncrementMessagesReceived(msgType string)                     {}
func (n *NullMetrics) RecordMessageProcessingTime(msgType string, d time.Duration)  {}
func (n *NullMetrics) IncrementViewChanges()                                        {}
func (n *NullMetrics) RecordBlockExecutionTime(d time.Duration)                     {}
func (n *NullMetrics) AddTransactions(count int)                                    {}
