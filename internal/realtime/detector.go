package realtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// MonitoredSource represents a URL source to monitor for changes.
type MonitoredSource struct {
	URL           string        `json:"url"`
	CheckInterval time.Duration `json:"check_interval"`
	SourceType    string        `json:"source_type"` // "bnm", "aaoifi"
	Category      string        `json:"category"`
	Name          string        `json:"name"`
	Enabled       bool          `json:"enabled"`
}

// DefaultBNMSources returns the default BNM sources to monitor.
func DefaultBNMSources() []MonitoredSource {
	return []MonitoredSource{
		{
			URL:           "https://www.bnm.gov.my/regulations/policy-documents",
			CheckInterval: 5 * time.Minute,
			SourceType:    "bnm",
			Category:      "policy_documents",
			Name:          "BNM Policy Documents",
			Enabled:       true,
		},
		{
			URL:           "https://www.bnm.gov.my/circulars",
			CheckInterval: 5 * time.Minute,
			SourceType:    "bnm",
			Category:      "circulars",
			Name:          "BNM Circulars",
			Enabled:       true,
		},
		{
			URL:           "https://www.bnm.gov.my/guidelines",
			CheckInterval: 5 * time.Minute,
			SourceType:    "bnm",
			Category:      "guidelines",
			Name:          "BNM Guidelines",
			Enabled:       true,
		},
		{
			URL:           "https://www.bnm.gov.my/islamic-finance",
			CheckInterval: 10 * time.Minute,
			SourceType:    "bnm",
			Category:      "islamic_finance",
			Name:          "BNM Islamic Finance",
			Enabled:       true,
		},
	}
}

// ChangeDetectorConfig holds configuration for the change detector.
type ChangeDetectorConfig struct {
	Sources           []MonitoredSource
	UserAgent         string
	RequestTimeout    time.Duration
	MaxRetries        int
	RetryDelay        time.Duration
	RateLimitPerHost  rate.Limit
	RateLimitBurst    int
	HashCachePrefix   string
	MetricsCachePrefix string
}

// DefaultChangeDetectorConfig returns sensible defaults.
func DefaultChangeDetectorConfig() ChangeDetectorConfig {
	return ChangeDetectorConfig{
		Sources:           DefaultBNMSources(),
		UserAgent:         "ShariaComply-Bot/1.0 (+https://shariahcomply.ai)",
		RequestTimeout:    30 * time.Second,
		MaxRetries:        3,
		RetryDelay:        5 * time.Second,
		RateLimitPerHost:  rate.Limit(1), // 1 request per second per host
		RateLimitBurst:    1,
		HashCachePrefix:   "content_hash:",
		MetricsCachePrefix: "detector_metrics:",
	}
}

// ChangeDetector monitors URLs for content changes.
type ChangeDetector struct {
	nats       *NATSClient
	redis      *redis.Client
	config     ChangeDetectorConfig
	logger     *slog.Logger
	httpClient *http.Client
	limiters   map[string]*rate.Limiter
	limiterMu  sync.RWMutex
	wg         sync.WaitGroup
	cancel     context.CancelFunc
	metrics    *DetectorMetrics
}

// DetectorMetrics holds metrics for the change detector.
type DetectorMetrics struct {
	mu               sync.RWMutex
	ChecksPerformed  map[string]int64
	ChangesDetected  map[string]int64
	Errors           map[string]int64
	LastCheckTime    map[string]time.Time
	LastChangeTime   map[string]time.Time
	AverageLatencyMs map[string]float64
}

// NewDetectorMetrics creates a new DetectorMetrics instance.
func NewDetectorMetrics() *DetectorMetrics {
	return &DetectorMetrics{
		ChecksPerformed:  make(map[string]int64),
		ChangesDetected:  make(map[string]int64),
		Errors:           make(map[string]int64),
		LastCheckTime:    make(map[string]time.Time),
		LastChangeTime:   make(map[string]time.Time),
		AverageLatencyMs: make(map[string]float64),
	}
}

// NewChangeDetector creates a new change detector.
func NewChangeDetector(
	natsClient *NATSClient,
	redisClient *redis.Client,
	cfg ChangeDetectorConfig,
	logger *slog.Logger,
) *ChangeDetector {
	if logger == nil {
		logger = slog.Default()
	}

	return &ChangeDetector{
		nats:   natsClient,
		redis:  redisClient,
		config: cfg,
		logger: logger.With("component", "change_detector"),
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		limiters: make(map[string]*rate.Limiter),
		metrics:  NewDetectorMetrics(),
	}
}

// Start begins monitoring all configured sources.
func (d *ChangeDetector) Start(ctx context.Context) error {
	ctx, d.cancel = context.WithCancel(ctx)

	d.logger.Info("starting change detector",
		"sources", len(d.config.Sources),
	)

	for _, source := range d.config.Sources {
		if !source.Enabled {
			d.logger.Debug("skipping disabled source", "url", source.URL)
			continue
		}

		d.wg.Add(1)
		go d.monitorSource(ctx, source)
	}

	d.logger.Info("change detector started")
	return nil
}

// Stop gracefully stops the change detector.
func (d *ChangeDetector) Stop(ctx context.Context) error {
	d.logger.Info("stopping change detector")

	if d.cancel != nil {
		d.cancel()
	}

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.logger.Info("change detector stopped gracefully")
	case <-ctx.Done():
		d.logger.Warn("change detector stop timed out")
		return ctx.Err()
	}

	return nil
}

// monitorSource continuously monitors a single source for changes.
func (d *ChangeDetector) monitorSource(ctx context.Context, source MonitoredSource) {
	defer d.wg.Done()

	d.logger.Info("starting source monitor",
		"url", source.URL,
		"interval", source.CheckInterval,
		"type", source.SourceType,
	)

	// Initial check
	d.checkForChanges(ctx, source)

	ticker := time.NewTicker(source.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Debug("stopping source monitor", "url", source.URL)
			return
		case <-ticker.C:
			d.checkForChanges(ctx, source)
		}
	}
}

// checkForChanges checks if a source URL has changed content.
func (d *ChangeDetector) checkForChanges(ctx context.Context, source MonitoredSource) {
	start := time.Now()
	defer func() {
		d.updateMetrics(source.URL, time.Since(start))
	}()

	// Apply rate limiting
	if err := d.waitForRateLimit(ctx, source.URL); err != nil {
		d.logger.Warn("rate limit wait cancelled", "url", source.URL, "error", err)
		return
	}

	// Fetch content with retry
	content, err := d.fetchWithRetry(ctx, source.URL)
	if err != nil {
		d.recordError(source.URL, err)
		return
	}

	// Calculate content hash
	hash := sha256.Sum256(content)
	currentHash := hex.EncodeToString(hash[:])

	// Get previous hash from Redis
	cacheKey := d.config.HashCachePrefix + source.URL
	previousHash, err := d.redis.Get(ctx, cacheKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		d.logger.Warn("failed to get previous hash from Redis",
			"url", source.URL,
			"error", err,
		)
	}

	// Check if content has changed
	if currentHash != previousHash {
		if previousHash != "" {
			// Content has actually changed (not first check)
			d.logger.Info("content change detected",
				"url", source.URL,
				"source_type", source.SourceType,
				"category", source.Category,
				"previous_hash", previousHash[:16]+"...",
				"current_hash", currentHash[:16]+"...",
			)

			// Publish change event
			if err := d.publishChangeEvent(ctx, source, currentHash); err != nil {
				d.logger.Error("failed to publish change event",
					"url", source.URL,
					"error", err,
				)
				d.recordError(source.URL, err)
				return
			}

			d.recordChange(source.URL)
		} else {
			d.logger.Debug("initial hash recorded", "url", source.URL)
		}

		// Update hash in Redis
		if err := d.redis.Set(ctx, cacheKey, currentHash, 0).Err(); err != nil {
			d.logger.Error("failed to update hash in Redis",
				"url", source.URL,
				"error", err,
			)
		}
	} else {
		d.logger.Debug("no change detected", "url", source.URL)
	}

	d.recordCheck(source.URL)
}

// fetchWithRetry fetches URL content with retry logic.
func (d *ChangeDetector) fetchWithRetry(ctx context.Context, url string) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= d.config.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d.config.RetryDelay * time.Duration(attempt)):
			}
		}

		content, err := d.fetchURL(ctx, url)
		if err == nil {
			return content, nil
		}

		lastErr = err
		d.logger.Warn("fetch attempt failed",
			"url", url,
			"attempt", attempt+1,
			"max_retries", d.config.MaxRetries,
			"error", err,
		)
	}

	return nil, fmt.Errorf("failed after %d retries: %w", d.config.MaxRetries, lastErr)
}

// fetchURL fetches content from a URL.
func (d *ChangeDetector) fetchURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", d.config.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Limit reading to 10MB to prevent memory issues
	content, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Normalize content to reduce false positives from dynamic content
	content = d.normalizeContent(content)

	return content, nil
}

// normalizeContent removes dynamic content that changes frequently.
func (d *ChangeDetector) normalizeContent(content []byte) []byte {
	s := string(content)

	// Remove common dynamic elements that don't indicate real changes
	// This is a simple approach - could be enhanced with HTML parsing
	dynamicPatterns := []string{
		"nonce=",
		"csrf_token",
		"timestamp",
		"cache_buster",
	}

	for _, pattern := range dynamicPatterns {
		if idx := strings.Index(s, pattern); idx != -1 {
			// Find end of the dynamic value
			end := idx + len(pattern)
			for end < len(s) && s[end] != '"' && s[end] != '\'' && s[end] != '&' && s[end] != ' ' {
				end++
			}
			s = s[:idx] + s[end:]
		}
	}

	return []byte(s)
}

// waitForRateLimit waits for the rate limiter to allow a request.
func (d *ChangeDetector) waitForRateLimit(ctx context.Context, url string) error {
	limiter := d.getLimiter(url)
	return limiter.Wait(ctx)
}

// getLimiter returns the rate limiter for a host.
func (d *ChangeDetector) getLimiter(url string) *rate.Limiter {
	// Extract host from URL
	host := url
	if idx := strings.Index(url, "://"); idx != -1 {
		host = url[idx+3:]
		if idx := strings.Index(host, "/"); idx != -1 {
			host = host[:idx]
		}
	}

	d.limiterMu.RLock()
	limiter, ok := d.limiters[host]
	d.limiterMu.RUnlock()

	if ok {
		return limiter
	}

	d.limiterMu.Lock()
	defer d.limiterMu.Unlock()

	// Double-check after acquiring write lock
	if limiter, ok = d.limiters[host]; ok {
		return limiter
	}

	limiter = rate.NewLimiter(d.config.RateLimitPerHost, d.config.RateLimitBurst)
	d.limiters[host] = limiter
	return limiter
}

// publishChangeEvent publishes a document changed event to NATS.
func (d *ChangeDetector) publishChangeEvent(ctx context.Context, source MonitoredSource, contentHash string) error {
	event := NewDocumentChangedEvent(
		source.URL,
		source.SourceType,
		source.Category,
		contentHash,
	)
	event.Metadata = map[string]interface{}{
		"source_name": source.Name,
		"check_interval": source.CheckInterval.String(),
	}

	return d.nats.PublishDocumentChanged(ctx, event)
}

// Metrics recording methods

func (d *ChangeDetector) recordCheck(url string) {
	d.metrics.mu.Lock()
	defer d.metrics.mu.Unlock()
	d.metrics.ChecksPerformed[url]++
	d.metrics.LastCheckTime[url] = time.Now()
}

func (d *ChangeDetector) recordChange(url string) {
	d.metrics.mu.Lock()
	defer d.metrics.mu.Unlock()
	d.metrics.ChangesDetected[url]++
	d.metrics.LastChangeTime[url] = time.Now()
}

func (d *ChangeDetector) recordError(url string, err error) {
	d.metrics.mu.Lock()
	defer d.metrics.mu.Unlock()
	d.metrics.Errors[url]++
	d.logger.Error("detector error", "url", url, "error", err)
}

func (d *ChangeDetector) updateMetrics(url string, latency time.Duration) {
	d.metrics.mu.Lock()
	defer d.metrics.mu.Unlock()

	// Update rolling average latency
	current := d.metrics.AverageLatencyMs[url]
	newLatency := float64(latency.Milliseconds())
	if current == 0 {
		d.metrics.AverageLatencyMs[url] = newLatency
	} else {
		// Exponential moving average
		d.metrics.AverageLatencyMs[url] = current*0.8 + newLatency*0.2
	}
}

// GetMetricsStruct returns a copy of the current metrics as a struct.
func (d *ChangeDetector) GetMetricsStruct() DetectorMetrics {
	d.metrics.mu.RLock()
	defer d.metrics.mu.RUnlock()

	// Create deep copy
	metrics := DetectorMetrics{
		ChecksPerformed:  make(map[string]int64),
		ChangesDetected:  make(map[string]int64),
		Errors:           make(map[string]int64),
		LastCheckTime:    make(map[string]time.Time),
		LastChangeTime:   make(map[string]time.Time),
		AverageLatencyMs: make(map[string]float64),
	}

	for k, v := range d.metrics.ChecksPerformed {
		metrics.ChecksPerformed[k] = v
	}
	for k, v := range d.metrics.ChangesDetected {
		metrics.ChangesDetected[k] = v
	}
	for k, v := range d.metrics.Errors {
		metrics.Errors[k] = v
	}
	for k, v := range d.metrics.LastCheckTime {
		metrics.LastCheckTime[k] = v
	}
	for k, v := range d.metrics.LastChangeTime {
		metrics.LastChangeTime[k] = v
	}
	for k, v := range d.metrics.AverageLatencyMs {
		metrics.AverageLatencyMs[k] = v
	}

	return metrics
}

// GetMetrics returns the current metrics as a map for JSON serialization.
func (d *ChangeDetector) GetMetrics() map[string]interface{} {
	d.metrics.mu.RLock()
	defer d.metrics.mu.RUnlock()

	var totalChecks, totalChanges, totalErrors int64
	for _, v := range d.metrics.ChecksPerformed {
		totalChecks += v
	}
	for _, v := range d.metrics.ChangesDetected {
		totalChanges += v
	}
	for _, v := range d.metrics.Errors {
		totalErrors += v
	}

	return map[string]interface{}{
		"total_checks":    totalChecks,
		"total_changes":   totalChanges,
		"total_errors":    totalErrors,
		"sources_count":   len(d.config.Sources),
		"active_monitors": len(d.metrics.ChecksPerformed),
	}
}

// AddSource adds a new source to monitor at runtime.
func (d *ChangeDetector) AddSource(ctx context.Context, source MonitoredSource) {
	d.config.Sources = append(d.config.Sources, source)

	if source.Enabled {
		d.wg.Add(1)
		go d.monitorSource(ctx, source)
		d.logger.Info("added new monitored source", "url", source.URL)
	}
}

// ForceCheck triggers an immediate check for a specific URL.
func (d *ChangeDetector) ForceCheck(ctx context.Context, url string) error {
	for _, source := range d.config.Sources {
		if source.URL == url {
			d.checkForChanges(ctx, source)
			return nil
		}
	}
	return fmt.Errorf("source not found: %s", url)
}
