// Package crawler provides web crawling functionality for BNM and other sources.
package crawler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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

// BNMCrawlerConfig holds configuration for the BNM crawler.
type BNMCrawlerConfig struct {
	BaseURL        string
	UserAgent      string
	RateLimit      int           // requests per second
	RequestTimeout time.Duration
	MaxRetries     int
	RetryDelay     time.Duration
}

// DefaultBNMCrawlerConfig returns default crawler configuration.
func DefaultBNMCrawlerConfig() BNMCrawlerConfig {
	return BNMCrawlerConfig{
		BaseURL:        "https://www.bnm.gov.my",
		UserAgent:      "ShariaComply-Bot/1.0 (+https://sharia-comply.com/bot)",
		RateLimit:      1,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     2 * time.Second,
	}
}

// CrawledDocument represents a document crawled from the web.
type CrawledDocument struct {
	URL           string                 `json:"url"`
	Title         string                 `json:"title"`
	Category      string                 `json:"category"`
	Content       string                 `json:"content"`
	HTMLContent   string                 `json:"html_content"`
	PDFLinks      []string               `json:"pdf_links"`
	PublishedDate time.Time              `json:"published_date"`
	CrawledAt     time.Time              `json:"crawled_at"`
	ContentHash   string                 `json:"content_hash"`
	Metadata      map[string]interface{} `json:"metadata"`
}

// CrawlState represents the state of a crawl session for resumption.
type CrawlState struct {
	ID            string              `json:"id"`
	Source        string              `json:"source"`
	StartedAt     time.Time           `json:"started_at"`
	LastUpdatedAt time.Time           `json:"last_updated_at"`
	ProcessedURLs map[string]struct{} `json:"processed_urls"`
	PendingURLs   []string            `json:"pending_urls"`
	Status        string              `json:"status"`
	ErrorCount    int                 `json:"error_count"`
	SuccessCount  int                 `json:"success_count"`
}

// BNMCrawler crawls Bank Negara Malaysia website for Islamic banking documents.
type BNMCrawler struct {
	config     BNMCrawlerConfig
	httpClient *http.Client
	rateLimiter *rate.Limiter
	storage    storage.ObjectStorage
	log        *logger.Logger
	state      *CrawlState
	stateMu    sync.RWMutex
}

// NewBNMCrawler creates a new BNM crawler instance.
func NewBNMCrawler(cfg BNMCrawlerConfig, store storage.ObjectStorage, log *logger.Logger) *BNMCrawler {
	if log == nil {
		log = logger.Default()
	}

	return &BNMCrawler{
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
		storage:    store,
		log:        log.WithComponent("bnm-crawler"),
	}
}

// BNMTargetPage represents a target page to crawl.
type BNMTargetPage struct {
	URL      string
	Category string
}

// GetTargetPages returns the list of BNM pages to crawl.
func (c *BNMCrawler) GetTargetPages() []BNMTargetPage {
	return []BNMTargetPage{
		{URL: c.config.BaseURL + "/regulations/policy-documents", Category: "policy-documents"},
		{URL: c.config.BaseURL + "/circulars", Category: "circulars"},
		{URL: c.config.BaseURL + "/islamic-banking", Category: "islamic-banking"},
	}
}

// Crawl crawls all BNM target pages and returns discovered documents.
func (c *BNMCrawler) Crawl(ctx context.Context) ([]CrawledDocument, error) {
	c.initState("bnm")
	defer c.completeState()

	var allDocs []CrawledDocument
	targets := c.GetTargetPages()

	for _, target := range targets {
		select {
		case <-ctx.Done():
			return allDocs, ctx.Err()
		default:
		}

		c.log.Info("crawling target page", "url", target.URL, "category", target.Category)

		docs, err := c.CrawlPage(ctx, target.URL, target.Category)
		if err != nil {
			c.log.WithError(err).Error("failed to crawl page", "url", target.URL)
			c.incrementError()
			continue
		}

		allDocs = append(allDocs, docs...)
		c.log.Info("crawled page successfully", "url", target.URL, "documents_found", len(docs))
	}

	return allDocs, nil
}

// CrawlPage crawls a single page and extracts documents.
func (c *BNMCrawler) CrawlPage(ctx context.Context, pageURL, category string) ([]CrawledDocument, error) {
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
		ContentHash: c.hashContent(body),
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

	// Extract additional metadata
	doc.Metadata["source"] = "BNM"
	doc.Metadata["crawled_at"] = doc.CrawledAt.Format(time.RFC3339)
	doc.Metadata["page_url"] = pageURL

	c.incrementSuccess()

	return []CrawledDocument{doc}, nil
}

// CrawlPolicyDocuments crawls the policy documents section.
func (c *BNMCrawler) CrawlPolicyDocuments(ctx context.Context) ([]CrawledDocument, error) {
	pageURL := c.config.BaseURL + "/regulations/policy-documents"
	return c.CrawlPage(ctx, pageURL, "policy-documents")
}

// CrawlCirculars crawls the circulars section.
func (c *BNMCrawler) CrawlCirculars(ctx context.Context) ([]CrawledDocument, error) {
	pageURL := c.config.BaseURL + "/circulars"
	return c.CrawlPage(ctx, pageURL, "circulars")
}

// CrawlIslamicBanking crawls the Islamic banking section.
func (c *BNMCrawler) CrawlIslamicBanking(ctx context.Context) ([]CrawledDocument, error) {
	pageURL := c.config.BaseURL + "/islamic-banking"
	return c.CrawlPage(ctx, pageURL, "islamic-banking")
}

// DownloadPDF downloads a PDF file from the given URL.
func (c *BNMCrawler) DownloadPDF(ctx context.Context, pdfURL string) ([]byte, error) {
	// Rate limiting
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	// Resolve relative URLs
	fullURL := c.resolveURL(pdfURL)

	c.log.Info("downloading PDF", "url", fullURL)

	body, err := c.fetchWithRetry(ctx, fullURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	c.log.Info("PDF downloaded successfully", "url", fullURL, "size_bytes", len(body))
	return body, nil
}

// DownloadAndStorePDF downloads a PDF and stores it in object storage.
func (c *BNMCrawler) DownloadAndStorePDF(ctx context.Context, pdfURL string) (string, error) {
	data, err := c.DownloadPDF(ctx, pdfURL)
	if err != nil {
		return "", err
	}

	if c.storage == nil {
		return "", fmt.Errorf("storage not configured")
	}

	// Generate storage path
	filename := extractFilenameFromURL(pdfURL)
	storagePath := storage.BuildOriginalPath("bnm", filename)

	// Upload to storage
	path, err := c.storage.UploadBytes(ctx, data, storagePath, "application/pdf")
	if err != nil {
		return "", fmt.Errorf("failed to upload PDF to storage: %w", err)
	}

	c.log.Info("PDF stored successfully", "path", path)
	return path, nil
}

// fetchWithRetry fetches a URL with retry logic and exponential backoff.
func (c *BNMCrawler) fetchWithRetry(ctx context.Context, targetURL string) ([]byte, error) {
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
func (c *BNMCrawler) fetch(ctx context.Context, targetURL string) ([]byte, error) {
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return body, nil
}

// resolveURL resolves a potentially relative URL to an absolute URL.
func (c *BNMCrawler) resolveURL(targetURL string) string {
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

// hashContent generates a SHA256 hash of the content.
func (c *BNMCrawler) hashContent(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// State management methods

func (c *BNMCrawler) initState(source string) {
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

func (c *BNMCrawler) completeState() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.Status = "completed"
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *BNMCrawler) isProcessed(url string) bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	if c.state == nil {
		return false
	}
	_, exists := c.state.ProcessedURLs[url]
	return exists
}

func (c *BNMCrawler) markProcessed(url string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.ProcessedURLs[url] = struct{}{}
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *BNMCrawler) incrementError() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.ErrorCount++
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *BNMCrawler) incrementSuccess() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.SuccessCount++
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

// GetState returns the current crawl state.
func (c *BNMCrawler) GetState() *CrawlState {
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
func (c *BNMCrawler) SetState(state *CrawlState) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	c.state = state
}

// Helper functions for HTML parsing

// extractTitle extracts the page title from HTML.
func extractTitle(html string) string {
	// Simple regex to extract title tag content
	re := regexp.MustCompile(`<title[^>]*>([^<]+)</title>`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// extractTextContent extracts visible text from HTML.
func extractTextContent(html string) string {
	// Remove script and style tags
	reScript := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	html = reScript.ReplaceAllString(html, "")

	reStyle := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	html = reStyle.ReplaceAllString(html, "")

	// Remove HTML comments
	reComments := regexp.MustCompile(`(?s)<!--.*?-->`)
	html = reComments.ReplaceAllString(html, "")

	// Remove all HTML tags
	reTags := regexp.MustCompile(`<[^>]+>`)
	text := reTags.ReplaceAllString(html, " ")

	// Decode common HTML entities
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")

	// Normalize whitespace
	reSpaces := regexp.MustCompile(`\s+`)
	text = reSpaces.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

// extractPDFLinks extracts all PDF links from HTML.
func extractPDFLinks(html, baseURL string) []string {
	var pdfLinks []string
	seen := make(map[string]struct{})

	// Match href attributes that end in .pdf (case insensitive)
	re := regexp.MustCompile(`href=["']([^"']*\.pdf)["']`)
	matches := re.FindAllStringSubmatch(strings.ToLower(html), -1)

	// Also match original case
	reOriginal := regexp.MustCompile(`(?i)href=["']([^"']*\.pdf)["']`)
	matchesOriginal := reOriginal.FindAllStringSubmatch(html, -1)

	for _, match := range matchesOriginal {
		if len(match) > 1 {
			link := match[1]

			// Resolve relative URLs
			if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
				base, err := url.Parse(baseURL)
				if err == nil {
					ref, err := url.Parse(link)
					if err == nil {
						link = base.ResolveReference(ref).String()
					}
				}
			}

			if _, exists := seen[link]; !exists {
				seen[link] = struct{}{}
				pdfLinks = append(pdfLinks, link)
			}
		}
	}

	// Suppress unused variable warning
	_ = matches

	return pdfLinks
}

// extractPublishDate attempts to extract a publish date from HTML.
func extractPublishDate(html string) time.Time {
	// Common date patterns
	patterns := []string{
		`(\d{1,2})\s+(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{4})`,
		`(\d{4})-(\d{2})-(\d{2})`,
		`(\d{2})/(\d{2})/(\d{4})`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(html)
		if len(matches) > 0 {
			// Try to parse the date
			dateStr := matches[0]

			// Try different date formats
			formats := []string{
				"2 January 2006",
				"02 January 2006",
				"2006-01-02",
				"02/01/2006",
			}

			for _, format := range formats {
				if t, err := time.Parse(format, dateStr); err == nil {
					return t
				}
			}
		}
	}

	return time.Time{}
}

// extractFilenameFromURL extracts a filename from a URL.
func extractFilenameFromURL(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Sprintf("document-%d.pdf", time.Now().Unix())
	}

	path := parsedURL.Path
	segments := strings.Split(path, "/")
	if len(segments) > 0 {
		filename := segments[len(segments)-1]
		if filename != "" {
			return filename
		}
	}

	return fmt.Sprintf("document-%d.pdf", time.Now().Unix())
}
