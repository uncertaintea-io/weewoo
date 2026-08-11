package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/collection"
	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
	"github.com/uncertaintea-io/weewoo/internal/migrations"
)

const (
	appServerWriteTimeout   = 20 * time.Second
	startupMigrationTimeout = 2 * time.Minute
)
const (
	sleep_duration = 1 * time.Second
	sleep_message  = "zzz\n"
)

func NewMetricshandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}

// SleepHandler returns a successful ping after the configured delay.
func SleepHandler(sleepTime time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		timer := time.NewTimer(sleepTime)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-r.Context().Done():
			slog.Error("request canceled", "error", r.Context().Err())
			return
		}

		if r.Context().Err() != nil {
			slog.Error("request context error", "error", r.Context().Err())
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sleep_message))
	})
}

type serviceResponse struct {
	ID              int            `json:"id"`
	Name            string         `json:"name"`
	PrometheusURL   string         `json:"prometheusUrl"`
	LoadQuery       string         `json:"loadQuery"`
	LatencyQuery    string         `json:"latencyQuery"`
	IntervalSeconds int64          `json:"intervalSeconds"`
	Revision        int64          `json:"revision"`
	Generation      int64          `json:"generation"`
	BaselineResetAt *time.Time     `json:"baselineResetAt,omitempty"`
	Tracking        trackingStatus `json:"tracking"`
	Imports         []importJob    `json:"imports"`
	TimeOfDayModel  modelStatus    `json:"timeOfDayModel"`
}

type modelStatus struct {
	State       string     `json:"state"`
	Coverage    float64    `json:"coverage"`
	Progress    float64    `json:"progress"`
	Required    int        `json:"requiredDays"`
	LatestBuild *time.Time `json:"latestBuild,omitempty"`
}

type createServiceRequest struct {
	Name            string     `json:"name"`
	PrometheusURL   string     `json:"prometheusUrl"`
	LoadQuery       string     `json:"loadQuery"`
	LatencyQuery    string     `json:"latencyQuery"`
	IntervalSeconds int64      `json:"intervalSeconds"`
	Revision        int64      `json:"revision,omitempty"`
	ImportStart     *time.Time `json:"importStart,omitempty"`
	ImportEnd       *time.Time `json:"importEnd,omitempty"`
}

type serviceCollector interface {
	Schedule(service *config.Service) error
	Unschedule(serviceID int)
	Import(ctx context.Context, service *config.Service, start, end time.Time, progress collection.ImportProgressHandler) (collection.ImportSummary, error)
}

type liveServiceTracker struct {
	collector       collection.Collector
	scheduler       *collection.IntervalScheduler
	scheduleBuilder func(serviceID int) error
}

func (t *liveServiceTracker) Schedule(service *config.Service) error {
	if err := t.collector.Schedule(service); err != nil {
		return fmt.Errorf("failed to schedule metric collection: %w", err)
	}
	if err := t.scheduleBuilder(service.Id); err != nil {
		return fmt.Errorf("failed to schedule ECDF publishing: %w", err)
	}
	return nil
}

func (t *liveServiceTracker) Import(ctx context.Context, service *config.Service, start, end time.Time, progress collection.ImportProgressHandler) (collection.ImportSummary, error) {
	return t.collector.Import(ctx, service, start, end, progress)
}

func (t *liveServiceTracker) Unschedule(serviceID int) {
	t.collector.Unschedule(serviceID)
	t.scheduler.RemoveCallback(collection.CallbackID(serviceID, collection.BuilderCallback))
}

func newServiceResponse(service *config.Service) serviceResponse {
	response := serviceResponse{
		ID:              service.Id,
		Name:            service.Name,
		PrometheusURL:   service.PrometheusURL,
		LoadQuery:       service.LoadQuery,
		LatencyQuery:    service.LatencyQuery,
		IntervalSeconds: int64(service.Interval / time.Second),
		Revision:        service.Revision,
		Generation:      service.Generation,
		Tracking:        trackingStatus{State: "pending", Activity: []activityEntry{}},
		Imports:         []importJob{},
		TimeOfDayModel:  modelStatus{State: "learning", Required: 5},
	}
	if !service.BaselineResetAt.IsZero() {
		resetAt := service.BaselineResetAt
		response.BaselineResetAt = &resetAt
	}
	return response
}

func registerAPIHandlers(mux *http.ServeMux, alerts, services http.Handler) {
	mux.Handle("/api/alerts", alerts)
	mux.Handle("/api/alerts/", alerts)
	mux.Handle("/api/", services)
}

func validateCreateService(request createServiceRequest) error {
	if request.Name == "" || request.PrometheusURL == "" || request.LoadQuery == "" || request.LatencyQuery == "" {
		return fmt.Errorf("name, prometheusUrl, loadQuery, and latencyQuery are required")
	}
	parsedURL, err := url.ParseRequestURI(request.PrometheusURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return fmt.Errorf("prometheusUrl must be an HTTP or HTTPS URL")
	}
	minimumIntervalSeconds := int64(config.MinimumServiceInterval / time.Second)
	if request.IntervalSeconds < minimumIntervalSeconds {
		return fmt.Errorf("intervalSeconds must be at least %d", minimumIntervalSeconds)
	}
	if (request.ImportStart == nil) != (request.ImportEnd == nil) {
		return fmt.Errorf("importStart and importEnd must be provided together")
	}
	if request.ImportStart != nil && !request.ImportStart.Before(*request.ImportEnd) {
		return fmt.Errorf("importStart must be before importEnd")
	}
	return nil
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// requestDuration tracks total app request latency in seconds.
var requestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace:                   "weewoo",
		Subsystem:                   "http",
		Name:                        "request_duration_seconds",
		Help:                        "HTTP request latency in seconds.",
		NativeHistogramBucketFactor: 1.1,
	},
	[]string{"status_code"},
)

// observeRequestDuration records how long the wrapped handler takes.
func observeRequestDuration(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 && r.Context().Err() != nil {
			// 499 is conventionally used in metrics for a request canceled by the client.
			status = 499
		} else if status == 0 {
			status = http.StatusOK
		}

		requestDuration.WithLabelValues(strconv.Itoa(status)).Observe(time.Since(start).Seconds())
	})
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	configfile := flag.String("config", "config.yaml", "Config file")
	flag.Parse()
	systemSettings, err := config.ReadSystemSettings(*configfile)
	if err != nil {
		log.Fatalf("Failed to read system settings: %v", err)
	}
	db, err := systemSettings.OpenDatabase()
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	migrationCtx, cancelMigrations := context.WithTimeout(context.Background(), startupMigrationTimeout)
	err = migrations.Apply(migrationCtx, db)
	cancelMigrations()
	if err != nil {
		log.Fatalf("Failed to apply database migrations: %v", err)
	}

	cfg := config.NewDatabaseConfig(db)
	defer cfg.Close()

	services, err := cfg.ReadAllServices()
	if err != nil {
		log.Fatalf("Failed to read services: %v", err)
	}
	monitor := newTrackingMonitor()
	scheduler := collection.NewIntervalScheduler()
	defer scheduler.Stop()
	chunkStore := ecdf.NewDatabaseChunkStore(db)
	jointStore := ecdf.NewDatabaseJointStore(db)
	alertManager := alerting.NewManager(db, cfg)
	alertDispatcher := alerting.NewOutboxDispatcher(db, cfg, alertManager)
	defer alertDispatcher.Stop()
	analysisWorker := collection.NewAnalysisWorker(cfg, jointStore, chunkStore, alertManager, collection.DefaultAnalysisQueueCapacity)
	defer analysisWorker.Stop()
	collector := collection.NewCollector(
		http.DefaultClient,
		chunkStore,
		scheduler,
		analysisWorker,
		collection.WithRecoveryQueue(db, cfg, alertManager),
		collection.WithCollectorEventHandler(monitor.handleCollectorEvent),
	)
	defer collector.Stop()
	for _, service := range services {
		if service.Paused {
			monitor.record(service.Id, "paused", "tracking_paused", "Prometheus collection is paused", time.Now().UTC())
			continue
		}
		if err := collector.Schedule(service); err != nil {
			log.Fatalf("Failed to schedule service %d: %v", service.Id, err)
		}
		monitor.activateRevision(service.Id, service.Revision)
	}

	// Start ECDF builder
	chunkStore = ecdf.NewDatabaseChunkStore(db)
	jointStore = ecdf.NewDatabaseJointStore(db)
	err = collection.StartECDFBuilder(chunkStore, jointStore, cfg, scheduler)
	if err != nil {
		log.Fatalf("Failed to start ECDF builder: %v", err)
	}
	tracker := &liveServiceTracker{
		collector: collector,
		scheduler: scheduler,
		scheduleBuilder: func(serviceID int) error {
			return collection.ScheduleECDFBuilder(serviceID, chunkStore, jointStore, cfg, scheduler)
		},
	}
	imports := newImportManager(tracker, monitor, func(ctx context.Context, serviceID int) error {
		return collection.BuildServiceECDFs(ctx, chunkStore, jointStore, cfg, serviceID, time.Now().UTC())
	}, &databaseImportJobStore{db: db})

	appMux := http.NewServeMux()
	registerAPIHandlers(
		appMux,
		observeRequestDuration(NewAlertAPIHandler(alertManager)),
		observeRequestDuration(NewServiceAPIHandler(serviceAPIOptions{
			Config:      cfg,
			Tracker:     tracker,
			Monitor:     monitor,
			Imports:     imports,
			HTTPClient:  http.DefaultClient,
			Alerts:      alertManager,
			ModelStatus: &databaseModelStatusReader{db: db, cfg: cfg, chunks: chunkStore},
		})),
	)
	appMux.Handle("/api/jecdf", observeRequestDuration(NewJointECDFAPIHandler(jointStore)))
	settingsHandler := observeRequestDuration(NewSettingsAPIHandler(cfg))
	appMux.Handle("/api/settings", settingsHandler)
	appMux.Handle("/api/settings/test", settingsHandler)
	//edit this to change the sleep time
	appMux.Handle("/sleep", observeRequestDuration(SleepHandler(sleep_duration)))
	//Serve files from static folder
	appMux.Handle("/", observeRequestDuration(http.FileServer(http.Dir("./ui/dist"))))

	monitorPort := ":5000"
	appPort := ":8080"
	fmt.Println("Server is running on port" + appPort)
	fmt.Println("Metrics are running on port" + monitorPort)

	appServer := &http.Server{
		Addr:           appPort,
		Handler:        appMux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   appServerWriteTimeout,
		MaxHeaderBytes: 1 << 20,
	}

	metricsServer := &http.Server{
		Addr:    monitorPort,
		Handler: NewMetricshandler(),
	}

	serverErr := make(chan error)
	go func() {
		serverErr <- metricsServer.ListenAndServe()
	}()

	go func() {
		serverErr <- appServer.ListenAndServe()
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-signalCtx.Done():

	case err := <-serverErr:
		log.Fatal(err)
	}

	appServer.Shutdown(context.Background())
	metricsServer.Shutdown(context.Background())
	for range 2 {
		if err := <-serverErr; err != nil {
			logger.Error("server shutdown", slog.Any("error", err))
		}
	}
}
