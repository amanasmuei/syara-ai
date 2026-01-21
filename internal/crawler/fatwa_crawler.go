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

// FatwaState represents Malaysian state identifiers for fatwa sources.
type FatwaState string

const (
	FatwaStateSelangor   FatwaState = "selangor"
	FatwaStateJohor      FatwaState = "johor"
	FatwaStatePenang     FatwaState = "penang"
	FatwaStateFederal    FatwaState = "federal"
	FatwaStatePerak      FatwaState = "perak"
	FatwaStateKedah      FatwaState = "kedah"
	FatwaStateKelantan   FatwaState = "kelantan"
	FatwaStateTerengganu FatwaState = "terengganu"
	FatwaStatePahang     FatwaState = "pahang"
	FatwaStateNSembilan  FatwaState = "nsembilan"
	FatwaStateMelaka     FatwaState = "melaka"
	FatwaStatePerlis     FatwaState = "perlis"
	FatwaStateSabah      FatwaState = "sabah"
	FatwaStateSarawak    FatwaState = "sarawak"
)

// AllFatwaStates returns all available Malaysian fatwa states.
func AllFatwaStates() []FatwaState {
	return []FatwaState{
		FatwaStateSelangor,
		FatwaStateJohor,
		FatwaStatePenang,
		FatwaStateFederal,
		FatwaStatePerak,
		FatwaStateKedah,
		FatwaStateKelantan,
		FatwaStateTerengganu,
		FatwaStatePahang,
		FatwaStateNSembilan,
		FatwaStateMelaka,
		FatwaStatePerlis,
		FatwaStateSabah,
		FatwaStateSarawak,
	}
}

// FatwaCrawlerConfig holds configuration for a state fatwa crawler.
type FatwaCrawlerConfig struct {
	State          FatwaState
	BaseURL        string
	UserAgent      string
	RateLimit      int           // requests per second
	RequestTimeout time.Duration
	MaxRetries     int
	RetryDelay     time.Duration
	// CSS selectors for extracting content (state-specific)
	ListSelector    string // CSS selector for fatwa list items
	TitleSelector   string // CSS selector for fatwa title
	ContentSelector string // CSS selector for fatwa content
	DateSelector    string // CSS selector for fatwa date
}

// StateFatwa represents a fatwa ruling from a Malaysian state authority.
type StateFatwa struct {
	FatwaNumber   string    `json:"fatwa_number"`
	Title         string    `json:"title"`
	State         string    `json:"state"`
	Category      string    `json:"category"` // muamalat, ibadah, aqidah, social
	IssuedDate    time.Time `json:"issued_date"`
	Content       string    `json:"content"`
	OriginalURL   string    `json:"original_url"`
	MuftiName     string    `json:"mufti_name,omitempty"`
	References    []string  `json:"references,omitempty"` // Quran, Hadith references
	PDFLink       string    `json:"pdf_link,omitempty"`
}

// FatwaCrawler crawls Malaysian state fatwa authority websites.
type FatwaCrawler struct {
	config      FatwaCrawlerConfig
	httpClient  *http.Client
	rateLimiter *rate.Limiter
	storage     storage.ObjectStorage
	log         *logger.Logger
	state       *CrawlState
	stateMu     sync.RWMutex
}

// DefaultFatwaCrawlerConfig returns default fatwa crawler configuration for a given state.
func DefaultFatwaCrawlerConfig(state FatwaState, baseURL string) FatwaCrawlerConfig {
	return FatwaCrawlerConfig{
		State:          state,
		BaseURL:        baseURL,
		UserAgent:      "ShariaComply-Bot/1.0 (+https://sharia-comply.com/bot)",
		RateLimit:      1,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     2 * time.Second,
	}
}

// Predefined state configurations
// Note: URLs need to be configured in environment variables

// SelangorFatwaConfig returns configuration for Selangor fatwa portal.
func SelangorFatwaConfig(baseURL string) FatwaCrawlerConfig {
	cfg := DefaultFatwaCrawlerConfig(FatwaStateSelangor, baseURL)
	cfg.ListSelector = ".fatwa-list"
	cfg.TitleSelector = ".fatwa-title"
	cfg.ContentSelector = ".fatwa-content"
	cfg.DateSelector = ".fatwa-date"
	return cfg
}

// JohorFatwaConfig returns configuration for Johor fatwa portal.
func JohorFatwaConfig(baseURL string) FatwaCrawlerConfig {
	cfg := DefaultFatwaCrawlerConfig(FatwaStateJohor, baseURL)
	cfg.ListSelector = ".fatwa-item"
	cfg.TitleSelector = "h2.title"
	cfg.ContentSelector = ".content"
	cfg.DateSelector = ".date"
	return cfg
}

// PenangFatwaConfig returns configuration for Penang fatwa portal.
func PenangFatwaConfig(baseURL string) FatwaCrawlerConfig {
	cfg := DefaultFatwaCrawlerConfig(FatwaStatePenang, baseURL)
	cfg.ListSelector = ".fatwa-entry"
	cfg.TitleSelector = ".entry-title"
	cfg.ContentSelector = ".entry-content"
	cfg.DateSelector = ".entry-date"
	return cfg
}

// FederalFatwaConfig returns configuration for Federal Territory fatwa portal.
func FederalFatwaConfig(baseURL string) FatwaCrawlerConfig {
	cfg := DefaultFatwaCrawlerConfig(FatwaStateFederal, baseURL)
	cfg.ListSelector = ".fatwa-row"
	cfg.TitleSelector = ".fatwa-heading"
	cfg.ContentSelector = ".fatwa-body"
	cfg.DateSelector = ".fatwa-timestamp"
	return cfg
}

// PerakFatwaConfig returns configuration for Perak fatwa portal.
func PerakFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStatePerak, baseURL)
}

// KedahFatwaConfig returns configuration for Kedah fatwa portal.
func KedahFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStateKedah, baseURL)
}

// KelantanFatwaConfig returns configuration for Kelantan fatwa portal.
func KelantanFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStateKelantan, baseURL)
}

// TerengganuFatwaConfig returns configuration for Terengganu fatwa portal.
func TerengganuFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStateTerengganu, baseURL)
}

// PahangFatwaConfig returns configuration for Pahang fatwa portal.
func PahangFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStatePahang, baseURL)
}

// NSembilanFatwaConfig returns configuration for Negeri Sembilan fatwa portal.
func NSembilanFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStateNSembilan, baseURL)
}

// MelakaFatwaConfig returns configuration for Melaka fatwa portal.
func MelakaFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStateMelaka, baseURL)
}

// PerlisFatwaConfig returns configuration for Perlis fatwa portal.
func PerlisFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStatePerlis, baseURL)
}

// SabahFatwaConfig returns configuration for Sabah fatwa portal.
func SabahFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStateSabah, baseURL)
}

// SarawakFatwaConfig returns configuration for Sarawak fatwa portal.
func SarawakFatwaConfig(baseURL string) FatwaCrawlerConfig {
	return DefaultFatwaCrawlerConfig(FatwaStateSarawak, baseURL)
}

// NewFatwaCrawler creates a new fatwa crawler instance.
func NewFatwaCrawler(cfg FatwaCrawlerConfig, store storage.ObjectStorage, log *logger.Logger) *FatwaCrawler {
	if log == nil {
		log = logger.Default()
	}

	// Create transport with connection management to prevent deadlocks
	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		DisableKeepAlives:   true, // Disable keep-alives to prevent connection reuse issues
		ForceAttemptHTTP2:   false,
	}

	return &FatwaCrawler{
		config: cfg,
		httpClient: &http.Client{
			Timeout:   cfg.RequestTimeout,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		rateLimiter: rate.NewLimiter(rate.Limit(cfg.RateLimit), 1),
		storage:     store,
		log:         log.WithComponent(fmt.Sprintf("fatwa-%s-crawler", cfg.State)),
	}
}

// SourceType returns the source type string for database storage.
func (c *FatwaCrawler) SourceType() string {
	return fmt.Sprintf("fatwa_%s", c.config.State)
}

// Crawl crawls the fatwa portal and returns discovered documents.
func (c *FatwaCrawler) Crawl(ctx context.Context) ([]CrawledDocument, error) {
	c.initState(c.SourceType())
	defer c.completeState()

	if c.config.BaseURL == "" {
		return nil, fmt.Errorf("base URL not configured for state: %s", c.config.State)
	}

	c.log.Info("crawling fatwa portal", "state", c.config.State, "url", c.config.BaseURL)

	docs, err := c.CrawlPage(ctx, c.config.BaseURL)
	if err != nil {
		c.log.WithError(err).Error("failed to crawl fatwa page", "state", c.config.State)
		c.incrementError()
		return nil, err
	}

	c.log.Info("crawled fatwa portal successfully", "state", c.config.State, "documents_found", len(docs))
	return docs, nil
}

// CrawlPage crawls a single fatwa page and extracts documents.
func (c *FatwaCrawler) CrawlPage(ctx context.Context, pageURL string) ([]CrawledDocument, error) {
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
		Category:    "fatwa",
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

	// Extract additional metadata specific to Fatwa
	doc.Metadata["source"] = "Fatwa"
	doc.Metadata["state"] = string(c.config.State)
	doc.Metadata["fatwa_state"] = string(c.config.State)
	doc.Metadata["document_type"] = "fatwa"
	doc.Metadata["crawled_at"] = doc.CrawledAt.Format(time.RFC3339)
	doc.Metadata["page_url"] = pageURL

	// Extract fatwa information
	fatwas := c.extractFatwas(string(body))
	if len(fatwas) > 0 {
		doc.Metadata["fatwas"] = fatwas
	}

	// Categorize content
	category := c.categorizeFatwa(doc.Content)
	doc.Metadata["fatwa_category"] = category

	c.incrementSuccess()

	return []CrawledDocument{doc}, nil
}

// extractFatwas extracts fatwa information from the HTML content.
func (c *FatwaCrawler) extractFatwas(html string) []StateFatwa {
	var fatwas []StateFatwa

	// Match fatwa numbers in various formats
	reFatwaNum := regexp.MustCompile(`(?i)(?:Fatwa\s+(?:No\.?|Bil\.?)\s*)?(\d+/\d+|\d+)`)
	matches := reFatwaNum.FindAllStringSubmatch(html, -1)

	seen := make(map[string]struct{})
	for _, match := range matches {
		if len(match) > 1 {
			fatwaNum := match[1]
			if _, exists := seen[fatwaNum]; !exists {
				seen[fatwaNum] = struct{}{}
				fatwas = append(fatwas, StateFatwa{
					FatwaNumber: fatwaNum,
					State:       string(c.config.State),
				})
			}
		}
	}

	return fatwas
}

// categorizeFatwa attempts to categorize a fatwa based on its content.
func (c *FatwaCrawler) categorizeFatwa(content string) string {
	contentLower := strings.ToLower(content)

	// Muamalat (financial/commercial) keywords
	muamalatKeywords := []string{
		"muamalat", "kewangan", "perbankan", "bank", "pinjaman", "loan",
		"jual beli", "trading", "insurans", "takaful", "pelaburan", "investment",
		"saham", "sukuk", "zakat", "harta", "property", "mortgage", "gadaian",
		"riba", "interest", "bunga", "hibah", "wakaf", "waqf",
	}

	// Ibadah (worship) keywords
	ibadahKeywords := []string{
		"ibadah", "solat", "prayer", "puasa", "fasting", "haji", "umrah",
		"zikir", "doa", "masjid", "mosque", "azan", "wudhu", "tayammum",
	}

	// Aqidah (faith/belief) keywords
	aqidahKeywords := []string{
		"aqidah", "akidah", "iman", "faith", "tauhid", "syirik", "bidaah",
		"kufur", "murtad", "sesat", "deviant",
	}

	// Social keywords
	socialKeywords := []string{
		"nikah", "marriage", "talak", "divorce", "cerai", "nafkah", "anak",
		"warisan", "inheritance", "faraid", "wasiat", "keluarga", "family",
	}

	// Count keyword matches
	muamalatCount := countKeywordMatches(contentLower, muamalatKeywords)
	ibadahCount := countKeywordMatches(contentLower, ibadahKeywords)
	aqidahCount := countKeywordMatches(contentLower, aqidahKeywords)
	socialCount := countKeywordMatches(contentLower, socialKeywords)

	// Return category with highest count
	maxCount := muamalatCount
	category := "muamalat"

	if ibadahCount > maxCount {
		maxCount = ibadahCount
		category = "ibadah"
	}
	if aqidahCount > maxCount {
		maxCount = aqidahCount
		category = "aqidah"
	}
	if socialCount > maxCount {
		category = "social"
	}

	if maxCount == 0 {
		return "general"
	}

	return category
}

// countKeywordMatches counts how many keywords from the list appear in the content.
func countKeywordMatches(content string, keywords []string) int {
	count := 0
	for _, keyword := range keywords {
		if strings.Contains(content, keyword) {
			count++
		}
	}
	return count
}

// DownloadPDF downloads a PDF file from the given URL.
func (c *FatwaCrawler) DownloadPDF(ctx context.Context, pdfURL string) ([]byte, error) {
	// Rate limiting
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	// Resolve relative URLs
	fullURL := c.resolveURL(pdfURL)

	c.log.Info("downloading fatwa PDF", "state", c.config.State, "url", fullURL)

	body, err := c.fetchWithRetry(ctx, fullURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	c.log.Info("fatwa PDF downloaded successfully", "url", fullURL, "size_bytes", len(body))
	return body, nil
}

// DownloadAndStorePDF downloads a PDF and stores it in object storage.
func (c *FatwaCrawler) DownloadAndStorePDF(ctx context.Context, pdfURL string) (string, error) {
	data, err := c.DownloadPDF(ctx, pdfURL)
	if err != nil {
		return "", err
	}

	if c.storage == nil {
		return "", fmt.Errorf("storage not configured")
	}

	// Generate storage path using source type (fatwa_selangor, fatwa_johor, etc.)
	filename := extractFilenameFromURL(pdfURL)
	storagePath := storage.BuildOriginalPath(c.SourceType(), filename)

	// Upload to storage
	path, err := c.storage.UploadBytes(ctx, data, storagePath, "application/pdf")
	if err != nil {
		return "", fmt.Errorf("failed to upload PDF to storage: %w", err)
	}

	c.log.Info("fatwa PDF stored successfully", "state", c.config.State, "path", path)
	return path, nil
}

// fetchWithRetry fetches a URL with retry logic and exponential backoff.
func (c *FatwaCrawler) fetchWithRetry(ctx context.Context, targetURL string) ([]byte, error) {
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
func (c *FatwaCrawler) fetch(ctx context.Context, targetURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5,ms;q=0.3") // Include Malay language

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
func (c *FatwaCrawler) resolveURL(targetURL string) string {
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

func (c *FatwaCrawler) initState(source string) {
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

func (c *FatwaCrawler) completeState() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.Status = "completed"
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *FatwaCrawler) isProcessed(url string) bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	if c.state == nil {
		return false
	}
	_, exists := c.state.ProcessedURLs[url]
	return exists
}

func (c *FatwaCrawler) markProcessed(url string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.ProcessedURLs[url] = struct{}{}
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *FatwaCrawler) incrementError() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.ErrorCount++
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

func (c *FatwaCrawler) incrementSuccess() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state != nil {
		c.state.SuccessCount++
		c.state.LastUpdatedAt = time.Now().UTC()
	}
}

// GetState returns the current crawl state.
func (c *FatwaCrawler) GetState() *CrawlState {
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
func (c *FatwaCrawler) SetState(state *CrawlState) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	c.state = state
}

// GetStateName returns the human-readable name for a state.
func GetStateName(state FatwaState) string {
	names := map[FatwaState]string{
		FatwaStateSelangor:   "Selangor",
		FatwaStateJohor:      "Johor",
		FatwaStatePenang:     "Penang",
		FatwaStateFederal:    "Federal Territory",
		FatwaStatePerak:      "Perak",
		FatwaStateKedah:      "Kedah",
		FatwaStateKelantan:   "Kelantan",
		FatwaStateTerengganu: "Terengganu",
		FatwaStatePahang:     "Pahang",
		FatwaStateNSembilan:  "Negeri Sembilan",
		FatwaStateMelaka:     "Melaka",
		FatwaStatePerlis:     "Perlis",
		FatwaStateSabah:      "Sabah",
		FatwaStateSarawak:    "Sarawak",
	}

	if name, ok := names[state]; ok {
		return name
	}
	return string(state)
}
