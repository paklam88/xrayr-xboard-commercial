package metrics

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

// Collector holds business metrics for XrayR nodes.
type Collector struct {
	activeUsers func() float64
	uplink      atomic.Uint64
	downlink    atomic.Uint64
}

// NewCollector creates a metrics collector. activeUsers may be nil.
func NewCollector(activeUsers func() float64) *Collector {
	if activeUsers == nil {
		activeUsers = func() float64 { return 0 }
	}
	return &Collector{activeUsers: activeUsers}
}

// AddTraffic accumulates successfully observed user traffic bytes.
func (c *Collector) AddTraffic(up, down int64) {
	if up > 0 {
		c.uplink.Add(uint64(up))
	}
	if down > 0 {
		c.downlink.Add(uint64(down))
	}
}

type trafficCollector struct {
	c    *Collector
	desc *prometheus.Desc
}

func (t *trafficCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- t.desc
}

func (t *trafficCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(t.desc, prometheus.CounterValue, float64(t.c.uplink.Load()), "uplink")
	ch <- prometheus.MustNewConstMetric(t.desc, prometheus.CounterValue, float64(t.c.downlink.Load()), "downlink")
}

// Server is an optional Prometheus HTTP exporter.
type Server struct {
	httpServer *http.Server
	mu         sync.Mutex
}

// Start launches /metrics on listen addr (e.g. "0.0.0.0:9091").
func Start(listen string, collector *Collector) (*Server, error) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	if collector != nil {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "xrayr_active_users",
			Help: "Number of active users currently loaded in node memory",
		}, collector.activeUsers))

		reg.MustRegister(&trafficCollector{
			c: collector,
			desc: prometheus.NewDesc(
				"xrayr_traffic_bytes_total",
				"Cumulative traffic bytes observed by XrayR before panel reporting",
				[]string{"direction"},
				nil,
			),
		})
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	srv := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s := &Server{httpServer: srv}
	go func() {
		log.Infof("Prometheus metrics listening on %s/metrics", listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("Prometheus metrics server error: %v", err)
		}
	}()
	return s, nil
}

// Close stops the metrics HTTP server.
func (s *Server) Close() error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}
