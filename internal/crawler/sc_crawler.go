// Package crawler provides web crawling functionality for various regulatory sources.
package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"golang.org/x/time/rate"
)

// SCCrawlerConfig holds configuration for the Securities Commission Malaysia crawler.
type SCCrawlerConfig struct {
	BaseURL        string
	UserAgent      string
	RateLimit      int           // requests per second
	RequestTimeout time.Duration
	MaxRetries     int
	RetryDelay     time.Duration
}

// DefaultSCCrawlerConfig returns default SC crawler configuration.
func DefaultSCCrawlerConfig() SCCrawlerConfig {
	return SCCrawlerConfig{
		BaseURL:        "https://www.sc.com.my",
		UserAgent:      "ShariaComply-Bot/1.0 (+https://sharia-comply.com/bot)",
		RateLimit:      1,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     2 * time.Second,
	}
}

// SCTargetPage represents a target page to crawl from SC website.
type SCTargetPage struct {
	URL          string
	Category     string
	DocumentType string // acts, guidelines, shariah_resolutions
}

// SCCrawler crawls Securities Commission Malaysia website for Islamic capital market documents.
type SCCrawler struct {
	config      SCCrawlerConfig
	httpClient  *http.Client
	rateLimiter *rate.Limiter
	storage     storage.ObjectStorage
	log         *logger.Logger
	state       *CrawlState
	stateMu     sync.RWMutex
}

// NewSCCrawler creates a new SC crawler instance.
func NewSCCrawler(cfg SCCrawlerConfig, store storage.ObjectStorage, log *logger.Logger) *SCCrawler {
	if log == nil {
		log = logger.Default()
	}

	return &SCCrawler{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		rateLimiter: rate.NewLimiter(rate.Limit(cfg.RateLimit), 1),
		storage:     store,
		log:         log.WithComponent("sc-crawler"),
	}
}

// GetTargetPages returns the list of SC pages to crawl.
func (c *SCCrawler) GetTargetPages() []SCTargetPage {
	return []SCTargetPage{
		{
			URL:          c.config.BaseURL + "/regulation/acts",
			Category:     "acts",
			DocumentType: "acts",
		},
		{
			URL:          c.config.BaseURL + "/regulation/guidelines",
			Category:     "guidelines",
			DocumentType: "guidelines",
		},
		{
			URL:          c.config.BaseURL + "/development/islamic-capital-market",
			Category:     "islamic-capital-market",
			DocumentType: "shariah_resolutions",
		},
	}
}

// Crawl crawls all SC target pages and returns discovered documents.
func (c *SCCrawler) Crawl(ctx context.Context) ([]CrawledDocument, error) {
	c.initState("sc")
	defer c.completeState()

	var allDocs []CrawledDocument
	targets := c.GetTargetPages()

	for _, target := range targets {
		select {
		case <-ctx.Done():
			return allDocs, ctx.Err()
		default:
		}

		c.log.Info("crawling SC target page", "url", target.URL, "category", target.Category)

		docs, err := c.CrawlPage(ctx, target.URL, target.Category, target.DocumentType)
		if err != nil {
			c.log.WithError(err).Error("failed to crawl SC page", "url", target.URL)
			c.incrementError()
			continue
		}

		allDocs = append(allDocs, docs...)
		c.log.Info("crawled SC page successfully", "url", target.URL, "documents_found", len(docs))
	}

	return allDocs, nil
}

// CrawlPage crawls a single SC page and extracts documents.
func (c *SCCrawler) CrawlPage(ctx context.Context, pageURL, category, documentType string) ([]CrawledDocument, error) {
	if c.isProcessed(pageURL) {
		c.log.Debug("skipping already processed URL", "url", pageURL)
		return nil, nil
	}

	// Rate limiting
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	// Fetch the page
	body, err := c.fetchWithRetry(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch page: %w", err)
	}

	c.markProcessed(pageURL)

	// Parse the HTML content
	doc := CrawledDocument{
		URL:         pageURL,
		Category:    category,
		HTMLContent: string(body),
		CrawledAt:   time.Now().UTC(),
		ContentHash: hashContent(body),
		Metadata:    make(map[string]interface{}),
	}

	// Extract title
	doc.Title = extractTitle(string(body))

	// Extract text content
	doc.Content = extractTextContent(string(body))

	// Extract PDF links
	doc.PDFLinks = extractPDFLinks(string(body), c.config.BaseURL)

	// Extract publish date if available
	doc.PublishedDate = extractPublishDate(string(body))

	// Extract additional metadata specific to SC
	doc.Metadata["source"] = "SC"
	doc.Metadata["document_type"] = documentType
	doc.Metadata["crawled_at"] = doc.CrawledAt.Format(time.RFC3339)
	doc.Metadata["page_url"] = pageURL

	// Extract act numbers if this is an acts page
	if documentType == "acts" {
		actNumbers := c.extractActNumbers(string(body))
		if len(actNumbers) > 0 {
			doc.Metadata["act_numbers"] = actNumbers
		}
	}

	// Extract SAC resolution info if this is shariah resolutions page
	if documentType == "shariah_resolutions" {
		resolutionInfo := c.extractSACResolutionInfo(string(body))
		if len(resolutionInfo) > 0 {
			doc.Metadata["sac_resolutions"] = resolutionInfo
		}
	}

	c.incrementSuccess()

	return []CrawledDocument{doc}, nil
}

// CrawlActs crawls the SC Acts section.
func (c *SCCrawler) CrawlActs(ctx context.Context) ([]CrawledDocument, error) {
	pageURL := c.config.BaseURL + "/regulation/acts"
	return c.CrawlPage(ctx, pageURL, "acts", "acts")
}

// CrawlGuidelines crawls the SC Guidelines section.
func (c *SCCrawler) CrawlGuidelines(ctx context.Context) ([]CrawledDocument, error) {
	pageURL := c.config.BaseURL + "/regulation/guidelines"
	return c.CrawlPage(ctx, pageURL, "guidelines", "guidelines")
}

// CrawlShariahResolutions crawls the SC Shariah Advisory Council resolutions.
func (c *SCCrawler) CrawlShariahResolutions(ctx context.Context) ([]CrawledDocument, error) {
	pageURL := c.config.BaseURL + "/development/islamic-capital-market"
	return c.CrawlPage(ctx, pageURL, "islamic-capital-market", "shariah_resolutions")
}

// DownloadPDF downloads a PDF file from the given URL.
func (c *SCCrawler) DownloadPDF(ctx context.Context, pdfURL string) ([]byte, error) {
	// Rate limiting
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	// Resolve relative URLs
	fullURL := c.resolveURL(pdfURL)

	c.log.Info("downloading SC PDF", "url", fullURL)

	body, err := c.fetchWithRetry(ctx, fullURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	c.log.Info("SC PDF downloaded successfully", "url", fullURL, "size_bytes", len(body))
	return body, nil
}

// DownloadAndStorePDF downloads a PDF and stores it in object storage.
func (c *SCCrawler) DownloadAndStorePDF(ctx context.Context, pdfURL string) (string, error) {
	data, err := c.DownloadPDF(ctx, pdfURL)
	if err != nil {
		return "", err
	}

	if c.storage == nil {
		return "", fmt.Errorf("storage not configured")
	}

	// Generate storage path
	filename := extractFilenameFromURL(pdfURL)
	storagePath := storage.BuildOriginalPath("sc", filename)

	// Upload to storage
	path, err := c.storage.UploadBytes(ctx, data, storagePath, "application/pdf")
	if err != nil {
		return "", fmt.Errorf("failed to upload PDF to storage: %w", err)
	}

	c.log.Info("SC PDF stored successfully", "path", path)
	return path, nil
}

// extractActNumbers extracts act numbers from the HTML content.
func (c *SCCrawler) extractActNumbers(html string) []string {
	var actNumbers []string
	seen := make(map[string]struct{})

	// Match patterns like "Act 671", "Act A1499", etc.
	re := regexp.MustCompile(`(?i)Act\s+([A-Z]?\d+)`)
	matches := re.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) > 1 {
			actNum := "Act " + match[1]
			if _, exists := seen[actNum]; !exists {
				seen[actNum] = struct{}{}
				actNumbers = append(actNumbers, actNum)
			}
		}
	}

	return actNumbers
}

// extractSACResolutionInfo extracts SAC resolution information from HTML.
func (c *SCCrawler) extractSACResolutionInfo(html string) []map[string]string {
	var resolutions []map[string]string

	// Match patterns like "The 296th Shariah Advisory Council", "288th SAC Meeting"
	re := regexp.MustCompile(`(?i)(?:The\s+)?(\d+)(?:st|nd|rd|th)\s+Shariah\s+Advisory\s+Council[^<]*?(?:Meeting)?[^<]*?(?:\(([^)]+)\))?`)
	matches := re.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		resolution := map[string]string{
			"meeting_number": match[1],
		}
		if len(match) > 2 && match[2] != "" {
			resolution["date"] = strings.TrimSpace(match[2])
		}
		resolutions = append(resolutions, resolution)
	}

	return resolutions
}

// fetchWithRetry fetches a URL with retry logic and exponential backoff.
func (c *SCCrawler) fetchWithRetry(ctx context.Context, targetURL string) ([]byte, error) {
	var lastErr error
	delay := c.config.RetryDelay

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			c.log.Debug("retrying request", "url", targetURL, "attempt", attempt, "delay", delay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2 // Exponential backoff
		}

		body, err := c.fetch(ctx, targetURL)
		if err == nil {
			return body, nil
		}

		lastErr = err
		c.log.WithError(err).Warn("request failed", "url", targetURL, "attempt", attempt)
	}

	return nil, fmt.Errorf("all retries failed: %w", lastErr)
}

// fetch performs a single HTTP GET request.
func (c *SCCrawler) fetch(ctx context.Context, targetURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := readAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return body, nil
}

// resolveURL resolves a potentially relative URL to an absolute URL.
func (c *SCCrawler) resolveURL(targetURL string) string {
	if strings.HasPrefix(targetURL, "http://") || strings.HasPrefix(targetURL, "https://") {
		return targetURL
	}

	base, err := url.Parse(c.config.BaseURL)
	if err != nil {
		return targetURL
	}

	ref, err := url.Parse(targetURL)
	if err != nil {
		return targetURL
	}

	return base.ResolveReference(ref).String()
}

// State management methods

func (c *SCCrawler) initState(source string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	c.state = &CrawlState{
		ID:            fmt.Sprintf("%s-%d", source, time.Now().Unix()),
		Source:        source,
		StartedAt:     time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
		ProcessedURLs: make(map[string]struct{}),
		PendingURLs:   []string{},
		Status:        "running",
	}
}

func (c *SCCrawler) completeState() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.Status = "completed"
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *SCCrawler) isProcessed(url string) bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	if c.state == nil {
		return false
	}
	_, exists := c.state.ProcessedURLs[url]
	return exists
}

func (c *SCCrawler) markProcessed(url string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.ProcessedURLs[url] = struct{}{}
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *SCCrawler) incrementError() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.ErrorCount++
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *SCCrawler) incrementSuccess() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.SuccessCount++
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

// GetState returns the current crawl state.
func (c *SCCrawler) GetState() *CrawlState {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	if c.state == nil {
		return nil
	}

	// Return a copy to avoid race conditions
	stateCopy := *c.state
	stateCopy.ProcessedURLs = make(map[string]struct{}, len(c.state.ProcessedURLs))
	for k, v := range c.state.ProcessedURLs {
		stateCopy.ProcessedURLs[k] = v
	}

	return &stateCopy
}

// SetState sets the crawl state (for resumption).
func (c *SCCrawler) SetState(state *CrawlState) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	c.state = state
}
