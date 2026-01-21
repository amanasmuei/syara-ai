// Package crawler provides the HTML processor for capturing web pages.
package crawler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
)

// HTMLProcessorConfig holds configuration for the HTML processor.
type HTMLProcessorConfig struct {
	Timeout         time.Duration
	WaitAfterLoad   time.Duration
	ViewportWidth   int
	ViewportHeight  int
	DeviceScaleFactor float64
	UserAgent       string
}

// DefaultHTMLProcessorConfig returns default configuration.
func DefaultHTMLProcessorConfig() HTMLProcessorConfig {
	return HTMLProcessorConfig{
		Timeout:         60 * time.Second,
		WaitAfterLoad:   2 * time.Second,
		ViewportWidth:   1920,
		ViewportHeight:  1080,
		DeviceScaleFactor: 1.0,
		UserAgent:       "ShariaComply-Bot/1.0 (+https://sharia-comply.com/bot)",
	}
}

// HTMLProcessor processes HTML pages using headless Chrome.
type HTMLProcessor struct {
	config  HTMLProcessorConfig
	storage storage.ObjectStorage
	log     *logger.Logger
}

// ProcessedHTML represents a processed HTML page.
type ProcessedHTML struct {
	ID            string                 `json:"id"`
	URL           string                 `json:"url"`
	Title         string                 `json:"title"`
	Content       string                 `json:"content"`
	HTMLContent   string                 `json:"html_content"`
	Screenshot    []byte                 `json:"-"`
	ScreenshotPath string                `json:"screenshot_path"`
	ScreenshotURL string                 `json:"screenshot_url"`
	PDFBytes      []byte                 `json:"-"`
	PDFPath       string                 `json:"pdf_path"`
	PDFURL        string                 `json:"pdf_url"`
	ProcessedAt   time.Time              `json:"processed_at"`
	Metadata      map[string]interface{} `json:"metadata"`
	Sections      []CapturedSection      `json:"sections,omitempty"`
}

// CapturedSection represents a captured section of a page.
type CapturedSection struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Content       string      `json:"content"`
	Screenshot    []byte      `json:"-"`
	ScreenshotPath string     `json:"screenshot_path"`
	ScreenshotURL string      `json:"screenshot_url"`
	BoundingBox   BoundingBox `json:"bounding_box"`
}

// BoundingBox represents a rectangular region on a page.
type BoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// NewHTMLProcessor creates a new HTML processor.
func NewHTMLProcessor(cfg HTMLProcessorConfig, store storage.ObjectStorage, log *logger.Logger) *HTMLProcessor {
	if log == nil {
		log = logger.Default()
	}

	return &HTMLProcessor{
		config:  cfg,
		storage: store,
		log:     log.WithComponent("html-processor"),
	}
}

// ProcessPage processes a web page and captures its content.
func (h *HTMLProcessor) ProcessPage(ctx context.Context, url string, metadata map[string]interface{}) (*ProcessedHTML, error) {
	h.log.Info("processing HTML page", "url", url)

	// Create browser context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	// Create Chrome allocator options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserAgent(h.config.UserAgent),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(timeoutCtx, opts...)
	defer allocCancel()

	// Create browser context
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	result := &ProcessedHTML{
		ID:          uuid.New().String(),
		URL:         url,
		ProcessedAt: time.Now().UTC(),
		Metadata:    metadata,
	}

	var htmlContent string
	var title string

	// Navigate and capture page
	err := chromedp.Run(browserCtx,
		// Set viewport
		emulation.SetDeviceMetricsOverride(
			int64(h.config.ViewportWidth),
			int64(h.config.ViewportHeight),
			h.config.DeviceScaleFactor,
			false,
		),
		// Navigate to URL
		chromedp.Navigate(url),
		// Wait for page to load
		chromedp.WaitReady("body"),
		chromedp.Sleep(h.config.WaitAfterLoad),
		// Get page title
		chromedp.Title(&title),
		// Get HTML content
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load page: %w", err)
	}

	result.Title = title
	result.HTMLContent = htmlContent
	result.Content = extractTextFromHTML(htmlContent)

	h.log.Info("page loaded", "url", url, "title", title)

	// Capture full-page screenshot
	screenshot, err := h.captureFullPageScreenshot(browserCtx)
	if err != nil {
		h.log.WithError(err).Warn("failed to capture screenshot")
	} else {
		result.Screenshot = screenshot
	}

	// Generate PDF
	pdfBytes, err := h.generatePDF(browserCtx)
	if err != nil {
		h.log.WithError(err).Warn("failed to generate PDF")
	} else {
		result.PDFBytes = pdfBytes
	}

	// Upload to storage if available
	if h.storage != nil {
		if err := h.uploadProcessedHTML(ctx, result); err != nil {
			h.log.WithError(err).Warn("failed to upload to storage")
		}
	}

	h.log.Info("page processed successfully", "url", url, "content_length", len(result.Content))

	return result, nil
}

// ProcessBNMPage processes a BNM page with section extraction.
func (h *HTMLProcessor) ProcessBNMPage(ctx context.Context, url string, docMetadata map[string]interface{}) (*ProcessedHTML, error) {
	result, err := h.ProcessPage(ctx, url, docMetadata)
	if err != nil {
		return nil, err
	}

	// Extract sections from BNM page structure
	sections := h.extractBNMSections(result.HTMLContent)
	result.Sections = sections

	return result, nil
}

// captureFullPageScreenshot captures a full-page screenshot.
func (h *HTMLProcessor) captureFullPageScreenshot(ctx context.Context) ([]byte, error) {
	var screenshot []byte

	err := chromedp.Run(ctx,
		chromedp.FullScreenshot(&screenshot, 90),
	)
	if err != nil {
		return nil, fmt.Errorf("screenshot capture failed: %w", err)
	}

	return screenshot, nil
}

// generatePDF generates a PDF from the current page.
func (h *HTMLProcessor) generatePDF(ctx context.Context) ([]byte, error) {
	var pdfBytes []byte

	err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			pdfBytes, _, err = page.PrintToPDF().
				WithPaperWidth(8.5).
				WithPaperHeight(11).
				WithMarginTop(0.5).
				WithMarginBottom(0.5).
				WithMarginLeft(0.5).
				WithMarginRight(0.5).
				WithPrintBackground(true).
				Do(ctx)
			return err
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("PDF generation failed: %w", err)
	}

	return pdfBytes, nil
}

// extractBNMSections extracts sections from BNM page HTML.
func (h *HTMLProcessor) extractBNMSections(html string) []CapturedSection {
	var sections []CapturedSection

	// Common BNM section patterns
	patterns := []struct {
		regex *regexp.Regexp
		name  string
	}{
		{regexp.MustCompile(`(?s)<div[^>]*class="[^"]*content[^"]*"[^>]*>(.*?)</div>`), "content"},
		{regexp.MustCompile(`(?s)<article[^>]*>(.*?)</article>`), "article"},
		{regexp.MustCompile(`(?s)<section[^>]*>(.*?)</section>`), "section"},
		{regexp.MustCompile(`(?s)<div[^>]*class="[^"]*policy[^"]*"[^>]*>(.*?)</div>`), "policy"},
	}

	for _, p := range patterns {
		matches := p.regex.FindAllStringSubmatch(html, -1)
		for i, match := range matches {
			if len(match) > 1 {
				content := extractTextFromHTML(match[1])
				if len(content) > 50 { // Only include meaningful sections
					section := CapturedSection{
						ID:      uuid.New().String(),
						Title:   fmt.Sprintf("%s-%d", p.name, i+1),
						Content: content,
					}
					sections = append(sections, section)
				}
			}
		}
	}

	return sections
}

// uploadProcessedHTML uploads processed HTML content to storage.
func (h *HTMLProcessor) uploadProcessedHTML(ctx context.Context, result *ProcessedHTML) error {
	if h.storage == nil {
		return nil
	}

	// Upload screenshot
	if len(result.Screenshot) > 0 {
		path := fmt.Sprintf("pages/html/%s/screenshot.png", result.ID)
		storedPath, err := h.storage.UploadBytes(ctx, result.Screenshot, path, "image/png")
		if err != nil {
			return fmt.Errorf("failed to upload screenshot: %w", err)
		}
		result.ScreenshotPath = storedPath

		url, err := h.storage.GetURL(ctx, storedPath)
		if err == nil {
			result.ScreenshotURL = url
		}
	}

	// Upload PDF
	if len(result.PDFBytes) > 0 {
		path := fmt.Sprintf("pages/html/%s/page.pdf", result.ID)
		storedPath, err := h.storage.UploadBytes(ctx, result.PDFBytes, path, "application/pdf")
		if err != nil {
			return fmt.Errorf("failed to upload PDF: %w", err)
		}
		result.PDFPath = storedPath

		url, err := h.storage.GetURL(ctx, storedPath)
		if err == nil {
			result.PDFURL = url
		}
	}

	return nil
}

// CaptureElement captures a screenshot of a specific element.
func (h *HTMLProcessor) CaptureElement(ctx context.Context, url, selector string) ([]byte, *BoundingBox, error) {
	h.log.Debug("capturing element", "url", url, "selector", selector)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	var screenshot []byte
	var box *BoundingBox

	err := chromedp.Run(browserCtx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(selector),
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Set placeholder bounding box - in production, use DOM.getBoxModel
			box = &BoundingBox{
				X: 0, Y: 0, Width: 800, Height: 600,
			}
			return nil
		}),
		chromedp.Screenshot(selector, &screenshot),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("element capture failed: %w", err)
	}

	return screenshot, box, nil
}

// ExtractDynamicContent extracts content that requires JavaScript rendering.
func (h *HTMLProcessor) ExtractDynamicContent(ctx context.Context, url string, waitSelector string) (string, error) {
	h.log.Debug("extracting dynamic content", "url", url, "wait_selector", waitSelector)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	var htmlContent string

	actions := []chromedp.Action{
		chromedp.Navigate(url),
	}

	if waitSelector != "" {
		actions = append(actions, chromedp.WaitVisible(waitSelector))
	} else {
		actions = append(actions, chromedp.WaitReady("body"))
	}

	actions = append(actions,
		chromedp.Sleep(h.config.WaitAfterLoad),
		chromedp.OuterHTML("html", &htmlContent),
	)

	if err := chromedp.Run(browserCtx, actions...); err != nil {
		return "", fmt.Errorf("failed to extract dynamic content: %w", err)
	}

	return extractTextFromHTML(htmlContent), nil
}

// extractTextFromHTML extracts clean text from HTML content.
func extractTextFromHTML(html string) string {
	// Remove script and style tags
	reScript := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	html = reScript.ReplaceAllString(html, "")

	reStyle := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	html = reStyle.ReplaceAllString(html, "")

	// Remove HTML comments
	reComments := regexp.MustCompile(`(?s)<!--.*?-->`)
	html = reComments.ReplaceAllString(html, "")

	// Remove noscript content
	reNoscript := regexp.MustCompile(`(?s)<noscript[^>]*>.*?</noscript>`)
	html = reNoscript.ReplaceAllString(html, "")

	// Replace block elements with newlines
	blockTags := []string{"div", "p", "br", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr", "td", "th", "article", "section", "header", "footer", "nav"}
	for _, tag := range blockTags {
		reOpen := regexp.MustCompile(fmt.Sprintf(`(?i)</?%s[^>]*>`, tag))
		html = reOpen.ReplaceAllString(html, "\n")
	}

	// Remove all remaining HTML tags
	reTags := regexp.MustCompile(`<[^>]+>`)
	text := reTags.ReplaceAllString(html, " ")

	// Decode common HTML entities
	replacements := map[string]string{
		"&nbsp;":  " ",
		"&amp;":   "&",
		"&lt;":    "<",
		"&gt;":    ">",
		"&quot;":  "\"",
		"&#39;":   "'",
		"&apos;":  "'",
		"&mdash;": "-",
		"&ndash;": "-",
		"&bull;":  "*",
		"&copy;":  "(c)",
		"&reg;":   "(R)",
		"&trade;": "(TM)",
	}

	for entity, replacement := range replacements {
		text = strings.ReplaceAll(text, entity, replacement)
	}

	// Handle numeric entities
	reNumericEntity := regexp.MustCompile(`&#(\d+);`)
	text = reNumericEntity.ReplaceAllStringFunc(text, func(match string) string {
		var num int
		fmt.Sscanf(match, "&#%d;", &num)
		if num > 0 && num < 128 {
			return string(rune(num))
		}
		return " "
	})

	// Normalize whitespace
	reSpaces := regexp.MustCompile(`[ \t]+`)
	text = reSpaces.ReplaceAllString(text, " ")

	reNewlines := regexp.MustCompile(`\n{3,}`)
	text = reNewlines.ReplaceAllString(text, "\n\n")

	// Trim whitespace from lines
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	text = strings.Join(lines, "\n")

	// Remove empty lines at start/end
	text = strings.TrimSpace(text)

	return text
}

// ScrollAndCapture scrolls through a page capturing screenshots at intervals.
func (h *HTMLProcessor) ScrollAndCapture(ctx context.Context, url string) ([][]byte, error) {
	h.log.Debug("scroll capturing page", "url", url)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	var screenshots [][]byte

	// Navigate to page
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(h.config.WaitAfterLoad),
	); err != nil {
		return nil, fmt.Errorf("failed to navigate: %w", err)
	}

	// Get page height
	var pageHeight int64
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`document.documentElement.scrollHeight`, &pageHeight),
	); err != nil {
		return nil, fmt.Errorf("failed to get page height: %w", err)
	}

	// Capture screenshots while scrolling
	viewportHeight := int64(h.config.ViewportHeight)
	for offset := int64(0); offset < pageHeight; offset += viewportHeight {
		var screenshot []byte

		if err := chromedp.Run(browserCtx,
			chromedp.Evaluate(fmt.Sprintf(`window.scrollTo(0, %d)`, offset), nil),
			chromedp.Sleep(500*time.Millisecond),
			chromedp.CaptureScreenshot(&screenshot),
		); err != nil {
			h.log.WithError(err).Warn("failed to capture screenshot at offset", "offset", offset)
			continue
		}

		screenshots = append(screenshots, screenshot)
	}

	return screenshots, nil
}

// WaitForSelector waits for a specific selector to appear on the page.
func WaitForSelector(ctx context.Context, url, selector string, timeout time.Duration) error {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	allocCtx, allocCancel := chromedp.NewExecAllocator(timeoutCtx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	return chromedp.Run(browserCtx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(selector),
	)
}

// IsJavaScriptRequired checks if a page requires JavaScript for content.
func IsJavaScriptRequired(html string) bool {
	// Check for common JavaScript-heavy indicators
	indicators := []string{
		"<noscript>",
		"__NEXT_DATA__",
		"window.__INITIAL_STATE__",
		"ng-app",
		"data-reactroot",
		"<div id=\"root\"></div>",
		"<div id=\"app\"></div>",
	}

	htmlLower := strings.ToLower(html)
	for _, indicator := range indicators {
		if strings.Contains(htmlLower, strings.ToLower(indicator)) {
			return true
		}
	}

	// Check if body is mostly empty (JavaScript content loading)
	reBody := regexp.MustCompile(`(?s)<body[^>]*>(.*?)</body>`)
	if match := reBody.FindStringSubmatch(html); len(match) > 1 {
		bodyText := extractTextFromHTML(match[1])
		if len(strings.TrimSpace(bodyText)) < 100 {
			return true
		}
	}

	return false
}
