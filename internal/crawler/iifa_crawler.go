// Package crawler provides web crawling functionality for various regulatory sources.
package crawler

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"golang.org/x/time/rate"
)

// IIFACrawlerConfig holds configuration for the IIFA (Majma Fiqh) crawler.
type IIFACrawlerConfig struct {
	BaseURL        string
	UserAgent      string
	RateLimit      int           // requests per second
	RequestTimeout time.Duration
	MaxRetries     int
	RetryDelay     time.Duration
}

// DefaultIIFACrawlerConfig returns default IIFA crawler configuration.
func DefaultIIFACrawlerConfig() IIFACrawlerConfig {
	return IIFACrawlerConfig{
		BaseURL:        "https://iifa-aifi.org",
		UserAgent:      "ShariaComply-Bot/1.0 (+https://sharia-comply.com/bot)",
		RateLimit:      1,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     2 * time.Second,
	}
}

// IIFAResolution represents a resolution from IIFA/Majma Fiqh.
type IIFAResolution struct {
	ResolutionNumber string    `json:"resolution_number"` // e.g., "267"
	Title            string    `json:"title"`
	Session          string    `json:"session"`  // OIC session number
	Year             int       `json:"year"`
	Topics           []string  `json:"topics"`
	Content          string    `json:"content"`
	PDFLink          string    `json:"pdf_link,omitempty"`
	OriginalURL      string    `json:"original_url"`
	PublishedDate    time.Time `json:"published_date"`
}

// IIFATargetPage represents a target page to crawl from IIFA website.
type IIFATargetPage struct {
	URL      string
	Category string
}

// IIFACrawler crawls International Islamic Fiqh Academy website for resolutions.
type IIFACrawler struct {
	config      IIFACrawlerConfig
	httpClient  *http.Client
	rateLimiter *rate.Limiter
	storage     storage.ObjectStorage
	log         *logger.Logger
	state       *CrawlState
	stateMu     sync.RWMutex
}

// NewIIFACrawler creates a new IIFA crawler instance.
func NewIIFACrawler(cfg IIFACrawlerConfig, store storage.ObjectStorage, log *logger.Logger) *IIFACrawler {
	if log == nil {
		log = logger.Default()
	}

	return &IIFACrawler{
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
		log:         log.WithComponent("iifa-crawler"),
	}
}

// GetTargetPages returns the list of IIFA pages to crawl.
func (c *IIFACrawler) GetTargetPages() []IIFATargetPage {
	return []IIFATargetPage{
		{
			URL:      c.config.BaseURL + "/en/resolutions",
			Category: "resolutions",
		},
	}
}

// Crawl crawls all IIFA target pages and returns discovered documents.
func (c *IIFACrawler) Crawl(ctx context.Context) ([]CrawledDocument, error) {
	c.initState("iifa")
	defer c.completeState()

	var allDocs []CrawledDocument

	// Start with the first page of resolutions
	baseURL := c.config.BaseURL + "/en/resolutions"
	page := 1
	maxPages := 50 // Safety limit

	for page <= maxPages {
		select {
		case <-ctx.Done():
			return allDocs, ctx.Err()
		default:
		}

		pageURL := baseURL
		if page > 1 {
			pageURL = fmt.Sprintf("%s/page/%d", baseURL, page)
		}

		c.log.Info("crawling IIFA resolutions page", "url", pageURL, "page", page)

		// First, get the listing page to extract resolution links
		resolutionLinks, hasNextPage, err := c.extractResolutionLinks(ctx, pageURL)
		if err != nil {
			c.log.WithError(err).Error("failed to extract resolution links", "url", pageURL)
			c.incrementError()
			break
		}

		if len(resolutionLinks) == 0 {
			c.log.Info("no more resolutions found", "page", page)
			break
		}

		// Crawl each individual resolution page
		for _, link := range resolutionLinks {
			select {
			case <-ctx.Done():
				return allDocs, ctx.Err()
			default:
			}

			if c.isProcessed(link.URL) {
				continue
			}

			doc, err := c.crawlResolutionPage(ctx, link)
			if err != nil {
				c.log.WithError(err).Warn("failed to crawl resolution", "url", link.URL)
				c.incrementError()
				continue
			}

			allDocs = append(allDocs, doc)
			c.incrementSuccess()
		}

		c.log.Info("crawled IIFA page successfully", "page", page, "resolutions_found", len(resolutionLinks))

		if !hasNextPage {
			break
		}
		page++
	}

	return allDocs, nil
}

// ResolutionLink holds information about a resolution link.
type ResolutionLink struct {
	URL              string
	Title            string
	ResolutionNumber string
}

// extractResolutionLinks extracts resolution links from a listing page.
func (c *IIFACrawler) extractResolutionLinks(ctx context.Context, pageURL string) ([]ResolutionLink, bool, error) {
	// Rate limiting
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, false, fmt.Errorf("rate limiter error: %w", err)
	}

	body, err := c.fetchWithRetry(ctx, pageURL)
	if err != nil {
		return nil, false, fmt.Errorf("failed to fetch page: %w", err)
	}

	html := string(body)
	var links []ResolutionLink
	seen := make(map[string]struct{})

	// Pattern to extract resolution links
	// Example: href="https://iifa-aifi.org/en/56095.html">Resolution No. 267(12/26) Shari'ah Ruling...
	reLink := regexp.MustCompile(`href="(https://iifa-aifi\.org/en/\d+\.html)"[^>]*>\s*Resolution\s+No\.?\s*(\d+)[^<]*([^<]+)`)
	matches := reLink.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) >= 4 {
			url := match[1]
			if _, exists := seen[url]; exists {
				continue
			}
			seen[url] = struct{}{}

			links = append(links, ResolutionLink{
				URL:              url,
				ResolutionNumber: match[2],
				Title:            strings.TrimSpace("Resolution No. " + match[2] + " " + match[3]),
			})
		}
	}

	// Also try simpler pattern for just resolution URLs
	reSimple := regexp.MustCompile(`href="(https://iifa-aifi\.org/en/(\d+)\.html)"`)
	simpleMatches := reSimple.FindAllStringSubmatch(html, -1)

	for _, match := range simpleMatches {
		if len(match) >= 2 {
			url := match[1]
			if _, exists := seen[url]; exists {
				continue
			}
			seen[url] = struct{}{}

			links = append(links, ResolutionLink{
				URL: url,
			})
		}
	}

	// Check if there's a next page
	hasNextPage := strings.Contains(html, `href="/en/resolutions/page/`) ||
		strings.Contains(html, `>Next<`) ||
		strings.Contains(html, `class="next"`)

	c.log.Debug("extracted resolution links", "count", len(links), "has_next", hasNextPage)
	return links, hasNextPage, nil
}

// crawlResolutionPage crawls an individual resolution page.
func (c *IIFACrawler) crawlResolutionPage(ctx context.Context, link ResolutionLink) (CrawledDocument, error) {
	// Rate limiting
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return CrawledDocument{}, fmt.Errorf("rate limiter error: %w", err)
	}

	body, err := c.fetchWithRetry(ctx, link.URL)
	if err != nil {
		return CrawledDocument{}, fmt.Errorf("failed to fetch resolution: %w", err)
	}

	c.markProcessed(link.URL)
	html := string(body)

	doc := CrawledDocument{
		URL:         link.URL,
		Category:    "resolution",
		HTMLContent: html,
		CrawledAt:   time.Now().UTC(),
		ContentHash: hashContent(body),
		Metadata:    make(map[string]any),
	}

	// Extract title from page
	doc.Title = extractTitle(html)
	if doc.Title == "" && link.Title != "" {
		doc.Title = link.Title
	}

	// Extract resolution number from title or link
	resNum := link.ResolutionNumber
	if resNum == "" {
		resNum = ExtractResolutionNumber(doc.Title)
	}

	// Extract text content (resolution text)
	doc.Content = extractTextContent(html)

	// Extract publish date
	doc.PublishedDate = extractPublishDate(html)

	// Set metadata
	doc.Metadata["source"] = "IIFA"
	doc.Metadata["document_type"] = "resolution"
	doc.Metadata["resolution_number"] = resNum
	doc.Metadata["crawled_at"] = doc.CrawledAt.Format(time.RFC3339)
	doc.Metadata["page_url"] = link.URL

	// Extract any PDF links from the resolution page
	doc.PDFLinks = extractPDFLinks(html, c.config.BaseURL)

	c.log.Info("crawled resolution", "url", link.URL, "resolution", resNum)
	return doc, nil
}

// CrawlPage crawls a single IIFA resolution page and extracts documents.
func (c *IIFACrawler) CrawlPage(ctx context.Context, pageURL, category string) ([]CrawledDocument, error) {
	link := ResolutionLink{URL: pageURL}
	doc, err := c.crawlResolutionPage(ctx, link)
	if err != nil {
		return nil, err
	}
	doc.Category = category
	return []CrawledDocument{doc}, nil
}

// CrawlResolutions crawls the IIFA resolutions section.
func (c *IIFACrawler) CrawlResolutions(ctx context.Context) ([]CrawledDocument, error) {
	pageURL := c.config.BaseURL + "/en/resolutions"
	return c.CrawlPage(ctx, pageURL, "resolutions")
}

// extractResolutions extracts resolution information from the HTML content.
func (c *IIFACrawler) extractResolutions(html string) []IIFAResolution {
	var resolutions []IIFAResolution

	// Match patterns like "Resolution No. 267", "Resolution No. 266 (11/26)"
	reResolution := regexp.MustCompile(`(?i)Resolution\s+No\.?\s*(\d+)(?:\s*\((\d+)/(\d+)\))?`)
	matches := reResolution.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		resolution := IIFAResolution{
			ResolutionNumber: match[1],
		}

		// Extract session info if available
		if len(match) > 2 && match[2] != "" {
			resolution.Session = fmt.Sprintf("Session %s", match[2])
		}

		resolutions = append(resolutions, resolution)
	}

	// Also extract resolution titles
	reTitles := regexp.MustCompile(`(?i)Resolution\s+No\.?\s*\d+[^<]*?(?:Shari.?ah\s+Ruling\s+on[^<]+)`)
	titleMatches := reTitles.FindAllString(html, -1)

	for i, title := range titleMatches {
		if i < len(resolutions) {
			resolutions[i].Title = strings.TrimSpace(title)
		}
	}

	return resolutions
}

// ExtractResolutionNumber extracts and normalizes a resolution number.
func ExtractResolutionNumber(text string) string {
	// Match various formats: "Resolution No. 267", "Res. 267", "267"
	re := regexp.MustCompile(`(?i)(?:Resolution\s+No\.?\s*|Res\.?\s*)?(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// DownloadPDF downloads a PDF file from the given URL.
func (c *IIFACrawler) DownloadPDF(ctx context.Context, pdfURL string) ([]byte, error) {
	// Rate limiting
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	// Resolve relative URLs
	fullURL := c.resolveURL(pdfURL)

	c.log.Info("downloading IIFA PDF", "url", fullURL)

	body, err := c.fetchWithRetry(ctx, fullURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	c.log.Info("IIFA PDF downloaded successfully", "url", fullURL, "size_bytes", len(body))
	return body, nil
}

// DownloadAndStorePDF downloads a PDF and stores it in object storage.
func (c *IIFACrawler) DownloadAndStorePDF(ctx context.Context, pdfURL string) (string, error) {
	data, err := c.DownloadPDF(ctx, pdfURL)
	if err != nil {
		return "", err
	}

	if c.storage == nil {
		return "", fmt.Errorf("storage not configured")
	}

	// Generate storage path
	filename := extractFilenameFromURL(pdfURL)
	storagePath := storage.BuildOriginalPath("iifa", filename)

	// Upload to storage
	path, err := c.storage.UploadBytes(ctx, data, storagePath, "application/pdf")
	if err != nil {
		return "", fmt.Errorf("failed to upload PDF to storage: %w", err)
	}

	c.log.Info("IIFA PDF stored successfully", "path", path)
	return path, nil
}

// fetchWithRetry fetches a URL with retry logic and exponential backoff.
func (c *IIFACrawler) fetchWithRetry(ctx context.Context, targetURL string) ([]byte, error) {
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
func (c *IIFACrawler) fetch(ctx context.Context, targetURL string) ([]byte, error) {
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
func (c *IIFACrawler) resolveURL(targetURL string) string {
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

func (c *IIFACrawler) initState(source string) {
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

func (c *IIFACrawler) completeState() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.Status = "completed"
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *IIFACrawler) isProcessed(url string) bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	if c.state == nil {
		return false
	}
	_, exists := c.state.ProcessedURLs[url]
	return exists
}

func (c *IIFACrawler) markProcessed(url string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.ProcessedURLs[url] = struct{}{}
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *IIFACrawler) incrementError() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.ErrorCount++
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *IIFACrawler) incrementSuccess() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.SuccessCount++
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

// GetState returns the current crawl state.
func (c *IIFACrawler) GetState() *CrawlState {
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
func (c *IIFACrawler) SetState(state *CrawlState) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	c.state = state
}

// CrawlWithRScript uses the R script for scraping IIFA website.
// This method is more reliable as R's chromote library handles JavaScript-heavy pages better.
// The R script must be installed with: install.packages(c("chromote", "rvest", "stringr"))
func (c *IIFACrawler) CrawlWithRScript(ctx context.Context, scriptPath string) ([]CrawledDocument, error) {
	c.initState("iifa-rscript")
	defer c.completeState()

	c.log.Info("starting IIFA crawl with R script", "script", scriptPath)

	// Create temp output directory
	outputDir, err := os.MkdirTemp("", "iifa_files_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	c.log.Info("using output directory", "dir", outputDir)

	linksFile := filepath.Join(outputDir, "links.txt")

	// Run the R script
	cmd := exec.CommandContext(ctx, "Rscript", scriptPath, outputDir, linksFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	c.log.Info("executing R script", "command", cmd.String())

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("R script failed: %w", err)
	}

	c.log.Info("R script completed successfully")

	// Read the links file
	links, err := c.readLinksFile(linksFile)
	if err != nil {
		c.log.WithError(err).Warn("failed to read links file, scanning directory instead")
	}

	// Also read PDF-specific links file if it exists
	pdfLinksFile := filepath.Join(outputDir, "pdf_links.txt")
	pdfLinks, _ := c.readLinksFile(pdfLinksFile)
	links = append(links, pdfLinks...)

	// Scan the output directory for downloaded PDFs
	pdfFiles, err := c.scanForPDFs(outputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to scan for PDFs: %w", err)
	}

	c.log.Info("found downloaded PDFs", "count", len(pdfFiles), "links_count", len(links))

	// Create CrawledDocument entries
	var documents []CrawledDocument
	for _, pdfPath := range pdfFiles {
		filename := filepath.Base(pdfPath)
		doc := CrawledDocument{
			Title:     strings.TrimSuffix(filename, ".pdf"),
			URL:       c.config.BaseURL + "/en/resolutions",
			Category:  "resolution",
			PDFLinks:  []string{pdfPath}, // Local path for now
			CrawledAt: time.Now().UTC(),
			Metadata: map[string]any{
				"source":        "IIFA",
				"document_type": "resolution",
				"local_path":    pdfPath,
			},
		}
		documents = append(documents, doc)
	}

	// Also add any links from the links file that weren't downloaded
	linkSet := make(map[string]struct{})
	for _, doc := range documents {
		for _, link := range doc.PDFLinks {
			linkSet[filepath.Base(link)] = struct{}{}
		}
	}
	for _, link := range links {
		filename := filepath.Base(link)
		if _, exists := linkSet[filename]; !exists {
			documents = append(documents, CrawledDocument{
				Title:     strings.TrimSuffix(filename, ".pdf"),
				URL:       link,
				Category:  "resolution",
				PDFLinks:  []string{link},
				CrawledAt: time.Now().UTC(),
				Metadata: map[string]any{
					"source":        "IIFA",
					"document_type": "resolution",
				},
			})
		}
	}

	c.log.Info("IIFA R script crawl complete", "total_documents", len(documents))
	return documents, nil
}

// readLinksFile reads URLs from a text file (one per line).
func (c *IIFACrawler) readLinksFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var links []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		link := strings.TrimSpace(scanner.Text())
		if link != "" {
			links = append(links, link)
		}
	}

	return links, scanner.Err()
}

// scanForPDFs scans a directory for PDF files.
func (c *IIFACrawler) scanForPDFs(dir string) ([]string, error) {
	var pdfFiles []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".pdf") {
			pdfFiles = append(pdfFiles, path)
		}
		return nil
	})

	return pdfFiles, err
}

// ProcessAndStoreLocalPDFs processes PDFs from local paths and stores them in object storage.
func (c *IIFACrawler) ProcessAndStoreLocalPDFs(ctx context.Context, documents []CrawledDocument) ([]string, error) {
	var storedPaths []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, 3)

	for _, doc := range documents {
		if len(doc.PDFLinks) == 0 {
			continue
		}

		for _, pdfLink := range doc.PDFLinks {
			wg.Add(1)
			go func(link string) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				var path string
				var err error

				// Check if it's a local file or URL
				if strings.HasPrefix(link, "/") || strings.HasPrefix(link, "./") || filepath.IsAbs(link) {
					// Local file - read and upload
					path, err = c.uploadLocalPDF(ctx, link)
				} else if strings.HasPrefix(link, "http") {
					// URL - download and upload
					if err := c.rateLimiter.Wait(ctx); err != nil {
						c.log.WithError(err).Error("rate limiter error", "url", link)
						return
					}
					path, err = c.DownloadAndStorePDF(ctx, link)
				} else {
					// Assume it's a relative local path
					path, err = c.uploadLocalPDF(ctx, link)
				}

				if err != nil {
					c.log.WithError(err).Error("failed to process PDF", "link", link)
					return
				}

				mu.Lock()
				storedPaths = append(storedPaths, path)
				mu.Unlock()

				c.log.Info("processed PDF", "link", link, "stored_path", path)
			}(pdfLink)
		}
	}

	wg.Wait()
	return storedPaths, nil
}

// uploadLocalPDF reads a local PDF file and uploads it to object storage.
func (c *IIFACrawler) uploadLocalPDF(ctx context.Context, localPath string) (string, error) {
	if c.storage == nil {
		return "", fmt.Errorf("storage not configured")
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to read local PDF: %w", err)
	}

	filename := filepath.Base(localPath)
	storagePath := storage.BuildOriginalPath("iifa", filename)

	path, err := c.storage.UploadBytes(ctx, data, storagePath, "application/pdf")
	if err != nil {
		return "", fmt.Errorf("failed to upload PDF to storage: %w", err)
	}

	c.log.Info("uploaded local PDF", "local", localPath, "storage", path)
	return path, nil
}
