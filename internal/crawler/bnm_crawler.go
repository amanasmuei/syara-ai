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
	"github.com/chromedp/chromedp"
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
	UseBrowser     bool          // Use headless browser for JS-rendered pages
}

// DefaultBNMCrawlerConfig returns default crawler configuration.
func DefaultBNMCrawlerConfig() BNMCrawlerConfig {
	return BNMCrawlerConfig{
		BaseURL:        "https://www.bnm.gov.my",
		UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		RateLimit:      1,
		RequestTimeout: 60 * time.Second,
		MaxRetries:     3,
		RetryDelay:     2 * time.Second,
		UseBrowser:     true, // Use headless browser to bypass Cloudflare
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
		{URL: c.config.BaseURL + "/banking-islamic-banking", Category: "policy-documents"},
		{URL: c.config.BaseURL + "/insurance-takaful", Category: "insurance-takaful"},
		{URL: c.config.BaseURL + "/development-financial-institutions", Category: "development-fi"},
		{URL: c.config.BaseURL + "/money-services-business", Category: "money-services"},
		{URL: c.config.BaseURL + "/intermediaries", Category: "intermediaries"},
		{URL: c.config.BaseURL + "/payment-systems", Category: "payment-systems"},
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

// PolicyDocument represents a policy document from the BNM table.
type PolicyDocument struct {
	Date     string `json:"date"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	PDFLink  string `json:"pdf_link"`
	PageURL  string `json:"page_url"`
}

// CrawlPolicyDocuments crawls the policy documents section (Banking & Islamic Banking).
func (c *BNMCrawler) CrawlPolicyDocuments(ctx context.Context) ([]CrawledDocument, error) {
	pageURL := c.config.BaseURL + "/banking-islamic-banking"
	return c.CrawlPage(ctx, pageURL, "policy-documents")
}

// CrawlPolicyDocumentsWithFilter crawls BNM policy documents by clicking the Policy Document filter.
// This method interacts with the page to:
// 1. Click the "Policy Document" checkbox filter (using input[name="pos"][value="Policy Document"])
// 2. Wait for the table to update
// 3. Paginate through all results by clicking numbered page buttons
// 4. Extract document links from table#filta
func (c *BNMCrawler) CrawlPolicyDocumentsWithFilter(ctx context.Context) ([]PolicyDocument, error) {
	c.initState("bnm-policy")
	defer c.completeState()

	pageURL := c.config.BaseURL + "/banking-islamic-banking"
	c.log.Info("crawling policy documents with filter", "url", pageURL)

	// Create browser context with options to bypass CloudFront WAF
	// Try non-headless mode for testing (change to true for production)
	headless := false // Set to false for debugging, true for production
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("no-first-run", true),
		// Use a common user agent string
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		chromedp.WindowSize(1920, 1080),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Set a long timeout for the entire operation
	browserCtx, cancel = context.WithTimeout(browserCtx, 5*time.Minute)
	defer cancel()

	var allDocuments []PolicyDocument

	// Navigate to the page and wait for initial load
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		// Wait for Cloudflare and initial page load (matching R script's Sys.sleep(5))
		chromedp.Sleep(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load page: %w", err)
	}

	// Debug: Check what's on the page after initial load
	var pageDebug map[string]interface{}
	err = chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			(function() {
				var info = {};
				info.url = window.location.href;
				info.title = document.title;
				info.bodyLength = document.body ? document.body.innerHTML.length : 0;
				info.hasCloudflare = document.body.innerHTML.includes('cloudflare') || document.body.innerHTML.includes('challenge');
				info.iframeCount = document.querySelectorAll('iframe').length;
				info.tableCount = document.querySelectorAll('table').length;
				info.hasFilta = document.querySelector('table#filta') !== null;
				info.hasDataTable = document.querySelector('table.dataTable') !== null;
				// Check for the filter checkbox
				info.hasFilterCheckbox = document.querySelector('input[name="pos"][value="Policy Document"]') !== null;
				// Get sample of body text
				info.bodySample = document.body ? document.body.innerText.substring(0, 500) : '';
				return info;
			})()
		`, &pageDebug),
	)
	if err != nil {
		c.log.WithError(err).Error("failed to get page debug info")
	} else {
		c.log.Info("page state after initial load", "debug", pageDebug)
	}

	c.log.Info("page loaded, clicking Policy Document filter")

	// Click the Policy Document checkbox using the exact selector from the R script
	// R script uses: input[name="pos"][value="Policy Document"]
	err = chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			var checkbox = document.querySelector('input[name="pos"][value="Policy Document"]');
			if(checkbox && !checkbox.checked) { checkbox.click(); }
			checkbox !== null;
		`, nil),
		// Wait for table to update after filter (matching R script's Sys.sleep(5))
		chromedp.Sleep(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to click Policy Document filter: %w", err)
	}

	c.log.Info("filter clicked, waiting for table update")

	// Get total number of pages (R script hardcodes 12, but we'll try to detect it)
	var totalPages int
	err = chromedp.Run(browserCtx,
		chromedp.Evaluate(`
			(function() {
				var btns = Array.from(document.querySelectorAll('a.paginate_button'));
				var pageNums = btns.map(b => parseInt(b.textContent.trim())).filter(n => !isNaN(n));
				return pageNums.length > 0 ? Math.max(...pageNums) : 12;
			})()
		`, &totalPages),
	)
	if err != nil || totalPages == 0 {
		totalPages = 12 // fallback to R script's hardcoded value
		c.log.Warn("could not detect total pages, using default", "total_pages", totalPages)
	}

	c.log.Info("detected pagination", "total_pages", totalPages)

	// Loop through all pages (matching R script approach)
	for pageNum := 1; pageNum <= totalPages; pageNum++ {
		c.log.Info("extracting links from page", "page", pageNum)

		// Wait for table to render (matching R script's Sys.sleep(2))
		err = chromedp.Run(browserCtx,
			chromedp.Sleep(2*time.Second),
		)
		if err != nil {
			c.log.WithError(err).Error("failed to wait for table", "page", pageNum)
			break
		}

		// Extract links from table#filta (matching R script approach)
		docs, err := c.extractTableLinks(browserCtx, pageURL)
		if err != nil {
			c.log.WithError(err).Error("failed to extract links from page", "page", pageNum)
			break
		}

		allDocuments = append(allDocuments, docs...)
		c.log.Info("extracted links from page", "page", pageNum, "count", len(docs))

		// Click next page if not last (matching R script approach)
		if pageNum < totalPages {
			nextPage := pageNum + 1
			err = chromedp.Run(browserCtx,
				chromedp.Evaluate(fmt.Sprintf(`
					(function() {
						var btns = Array.from(document.querySelectorAll('a.paginate_button'));
						var next = btns.find(b => b.textContent.trim() == '%d');
						if(next) { next.click(); return true; }
						return false;
					})()
				`, nextPage), nil),
				// Wait for new page to render (matching R script's Sys.sleep(3))
				chromedp.Sleep(3*time.Second),
			)
			if err != nil {
				c.log.WithError(err).Warn("failed to click page button", "target_page", nextPage)
				break
			}
		}
	}

	// Remove duplicates and empty entries
	allDocuments = c.deduplicateDocuments(allDocuments)

	c.log.Info("policy documents crawl complete", "total_documents", len(allDocuments))
	return allDocuments, nil
}

// extractTableLinks extracts document links from table#filta (matching R script approach).
func (c *BNMCrawler) extractTableLinks(ctx context.Context, pageURL string) ([]PolicyDocument, error) {
	var documents []PolicyDocument

	// First, debug: check what tables exist and their structure
	var debugInfo map[string]interface{}
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				var info = {};
				// Check for table#filta
				var filta = document.querySelector('table#filta');
				info.hasFilta = filta !== null;

				// Check for any DataTable
				var dataTable = document.querySelector('table.dataTable');
				info.hasDataTable = dataTable !== null;

				// Get all table IDs
				var tables = document.querySelectorAll('table');
				info.tableIds = Array.from(tables).map(t => t.id || '(no id)');
				info.tableCount = tables.length;

				// Try to get row count from any table with tbody
				if(filta) {
					info.filtaRowCount = filta.querySelectorAll('tbody tr').length;
					info.filtaLinkCount = filta.querySelectorAll('tbody a').length;
				}
				if(dataTable) {
					info.dataTableRowCount = dataTable.querySelectorAll('tbody tr').length;
					info.dataTableLinkCount = dataTable.querySelectorAll('tbody a').length;
				}

				// Get first few links from any table
				var anyTable = filta || dataTable || tables[0];
				if(anyTable) {
					var links = anyTable.querySelectorAll('tbody a');
					info.sampleLinks = Array.from(links).slice(0, 3).map(a => a.getAttribute('href'));
				}

				return info;
			})()
		`, &debugInfo),
	)
	if err != nil {
		c.log.WithError(err).Error("failed to get debug info")
	} else {
		c.log.Info("table debug info", "info", debugInfo)
	}

	// JavaScript to extract links - try multiple selectors
	var links []string
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				// Try table#filta first (R script approach)
				var table = document.querySelector('table#filta');
				// Fallback to DataTable
				if(!table) table = document.querySelector('table.dataTable');
				// Fallback to first table
				if(!table) table = document.querySelector('table');

				if(!table) return [];

				var anchors = table.querySelectorAll('tbody a');
				var hrefs = [];
				anchors.forEach(function(a) {
					var href = a.getAttribute('href');
					if(href && href !== '') {
						hrefs.push(href);
					}
				});
				return hrefs;
			})()
		`, &links),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to extract table links: %w", err)
	}

	c.log.Info("extracted links", "count", len(links))

	for _, link := range links {
		if link == "" {
			continue
		}

		// Resolve relative URLs (matching R script's normalization)
		fullURL := c.resolveURL(link)

		doc := PolicyDocument{
			PDFLink: fullURL,
			PageURL: pageURL,
		}
		documents = append(documents, doc)
	}

	return documents, nil
}

// deduplicateDocuments removes duplicate documents based on PDFLink.
func (c *BNMCrawler) deduplicateDocuments(docs []PolicyDocument) []PolicyDocument {
	seen := make(map[string]struct{})
	var result []PolicyDocument

	for _, doc := range docs {
		if doc.PDFLink == "" {
			continue
		}
		if _, exists := seen[doc.PDFLink]; !exists {
			seen[doc.PDFLink] = struct{}{}
			result = append(result, doc)
		}
	}

	return result
}


// DownloadAllPolicyDocuments downloads all PDFs from the crawled policy documents.
func (c *BNMCrawler) DownloadAllPolicyDocuments(ctx context.Context, documents []PolicyDocument) ([]string, error) {
	var downloadedPaths []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Limit concurrent downloads
	semaphore := make(chan struct{}, 3)

	for _, doc := range documents {
		if doc.PDFLink == "" {
			continue
		}

		wg.Add(1)
		go func(d PolicyDocument) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Rate limiting
			if err := c.rateLimiter.Wait(ctx); err != nil {
				c.log.WithError(err).Error("rate limiter error", "url", d.PDFLink)
				return
			}

			path, err := c.DownloadAndStorePDF(ctx, d.PDFLink)
			if err != nil {
				c.log.WithError(err).Error("failed to download PDF", "url", d.PDFLink, "title", d.Title)
				c.incrementError()
				return
			}

			mu.Lock()
			downloadedPaths = append(downloadedPaths, path)
			mu.Unlock()

			c.incrementSuccess()
			c.log.Info("downloaded PDF", "title", d.Title, "path", path)
		}(doc)
	}

	wg.Wait()
	return downloadedPaths, nil
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
	// Use browser mode for BNM pages to bypass Cloudflare
	if c.config.UseBrowser {
		return c.fetchWithBrowserRetry(ctx, targetURL)
	}

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

// fetchWithBrowserRetry fetches using browser with retry logic.
func (c *BNMCrawler) fetchWithBrowserRetry(ctx context.Context, targetURL string) ([]byte, error) {
	var lastErr error
	delay := c.config.RetryDelay

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			c.log.Debug("retrying browser request", "url", targetURL, "attempt", attempt, "delay", delay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}

		body, err := c.fetchWithBrowser(ctx, targetURL)
		if err == nil {
			return body, nil
		}

		lastErr = err
		c.log.WithError(err).Warn("browser request failed", "url", targetURL, "attempt", attempt)
	}

	return nil, fmt.Errorf("all browser retries failed: %w", lastErr)
}

// fetch performs a single HTTP GET request.
func (c *BNMCrawler) fetch(ctx context.Context, targetURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers to mimic a real browser
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Cache-Control", "max-age=0")

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

// fetchWithBrowser uses chromedp headless browser to fetch JavaScript-rendered pages.
func (c *BNMCrawler) fetchWithBrowser(ctx context.Context, targetURL string) ([]byte, error) {
	c.log.Info("fetching with headless browser", "url", targetURL)

	// Create a new browser context - use longer timeout for Cloudflare
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent(c.config.UserAgent),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Set a longer timeout (90 seconds) for Cloudflare challenge
	browserCtx, cancel = context.WithTimeout(browserCtx, 90*time.Second)
	defer cancel()

	var htmlContent string
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(targetURL),
		// Wait for page to be ready (wait for body to exist)
		chromedp.WaitReady("body"),
		// Sleep to allow Cloudflare challenge and JS rendering
		chromedp.Sleep(5*time.Second),
		// Try to wait for table but don't fail if not found
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Try waiting for table, but continue even if it fails
			_ = chromedp.WaitVisible(`table.dataTable`, chromedp.ByQuery).Do(ctx)
			return nil
		}),
		chromedp.Sleep(2*time.Second),
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return nil, fmt.Errorf("browser fetch failed: %w", err)
	}

	c.log.Info("browser fetch successful", "url", targetURL, "content_length", len(htmlContent))
	return []byte(htmlContent), nil
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
