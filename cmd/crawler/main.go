// Package main is the entry point for the data ingestion crawler CLI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/chunker"
	"github.com/alqutdigital/islamic-banking-agent/internal/config"
	"github.com/alqutdigital/islamic-banking-agent/internal/crawler"
	"github.com/alqutdigital/islamic-banking-agent/internal/embedder"
	"github.com/alqutdigital/islamic-banking-agent/internal/processor"
	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

// Version information (set during build)
var (
	Version   = "dev"
	BuildTime = "unknown"
)

// IngestionOrchestrator coordinates the full ingestion pipeline.
type IngestionOrchestrator struct {
	cfg       *config.Config
	log       *logger.Logger
	storage   storage.ObjectStorage
	crawler   *crawler.BNMCrawler
	pdfProc   *processor.PDFProcessor
	chunker   *chunker.SemanticChunker
	embedder  embedder.Embedder
	stats     *IngestionStats
}

// IngestionStats tracks ingestion statistics.
type IngestionStats struct {
	StartTime          time.Time     `json:"start_time"`
	EndTime            time.Time     `json:"end_time"`
	Duration           time.Duration `json:"duration"`
	DocumentsFound     int64         `json:"documents_found"`
	DocumentsProcessed int64         `json:"documents_processed"`
	PDFsDownloaded     int64         `json:"pdfs_downloaded"`
	PagesProcessed     int64         `json:"pages_processed"`
	ChunksCreated      int64         `json:"chunks_created"`
	EmbeddingsGenerated int64        `json:"embeddings_generated"`
	Errors             int64         `json:"errors"`
	SkippedDocuments   int64         `json:"skipped_documents"`
}

// IngestOptions holds options for the ingest command.
type IngestOptions struct {
	Source     string
	Category   string
	URL        string
	InputFile  string
	OutputDir  string
	DryRun     bool
	Force      bool
	Workers    int
	SkipEmbed  bool
	State      string // For fatwa sources: selangor, johor, penang, federal, etc.
	UseRScript bool   // Use R script for BNM crawling (more reliable)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Create root command
	rootCmd := &cobra.Command{
		Use:     "crawler",
		Short:   "ShariaComply Data Ingestion CLI",
		Long:    "CLI tool for crawling, processing, and indexing Islamic banking documents.",
		Version: fmt.Sprintf("%s (built %s)", Version, BuildTime),
	}

	// Add subcommands
	rootCmd.AddCommand(newIngestCmd())
	rootCmd.AddCommand(newEmbedCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newProcessCmd())

	return rootCmd.Execute()
}

// newIngestCmd creates the ingest subcommand.
func newIngestCmd() *cobra.Command {
	opts := &IngestOptions{}

	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest documents from a source",
		Long:  "Crawl, process, chunk, and embed documents from various Shariah standards sources.",
		Example: `  # Ingest from BNM website
  crawler ingest --source=bnm --category=policy-documents

  # Ingest from BNM using R script (more reliable)
  crawler ingest --source=bnm --category=policy-documents --use-rscript

  # Ingest from Securities Commission Malaysia
  crawler ingest --source=sc --category=shariah_resolutions

  # Ingest from IIFA (Majma Fiqh)
  crawler ingest --source=iifa

  # Ingest state fatwa (Selangor)
  crawler ingest --source=fatwa --state=selangor

  # Ingest all state fatwas
  crawler ingest --source=fatwa --state=all

  # Ingest a specific PDF file
  crawler ingest --input=/path/to/document.pdf

  # Ingest from URL
  crawler ingest --url=https://example.com/document.pdf

  # Dry run (no storage)
  crawler ingest --source=bnm --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIngest(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Source, "source", "s", "", "Source to ingest: 'bnm', 'aaoifi', 'sc', 'iifa', or 'fatwa'")
	cmd.Flags().StringVarP(&opts.Category, "category", "c", "", "Category filter (e.g., 'policy-documents', 'circulars', 'shariah_resolutions')")
	cmd.Flags().StringVar(&opts.State, "state", "", "State for fatwa sources (selangor, johor, penang, federal, all)")
	cmd.Flags().StringVarP(&opts.URL, "url", "u", "", "Specific URL to ingest")
	cmd.Flags().StringVarP(&opts.InputFile, "input", "i", "", "Input file path (for local PDF processing)")
	cmd.Flags().StringVarP(&opts.OutputDir, "output", "o", "./data", "Output directory for processed data")
	cmd.Flags().BoolVarP(&opts.DryRun, "dry-run", "n", false, "Perform a dry run without saving")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force re-processing of existing documents")
	cmd.Flags().IntVarP(&opts.Workers, "workers", "w", 4, "Number of concurrent workers")
	cmd.Flags().BoolVar(&opts.SkipEmbed, "skip-embed", false, "Skip embedding generation")
	cmd.Flags().BoolVar(&opts.UseRScript, "use-rscript", false, "Use R script for BNM crawling (more reliable, requires R with chromote)")

	return cmd
}

// newEmbedCmd creates the embed subcommand.
func newEmbedCmd() *cobra.Command {
	var source string
	var force bool
	var batchSize int

	cmd := &cobra.Command{
		Use:   "embed",
		Short: "Generate embeddings for existing documents",
		Long:  "Generate or regenerate embeddings for documents that have been processed.",
		Example: `  # Embed all documents from a source
  crawler embed --source=bnm

  # Force re-embed existing
  crawler embed --source=aaoifi --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEmbed(cmd.Context(), source, force, batchSize)
		},
	}

	cmd.Flags().StringVarP(&source, "source", "s", "", "Source to embed (required)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force re-embedding of existing embeddings")
	cmd.Flags().IntVarP(&batchSize, "batch-size", "b", 50, "Batch size for embedding")
	cmd.MarkFlagRequired("source")

	return cmd
}

// newStatusCmd creates the status subcommand.
func newStatusCmd() *cobra.Command {
	var source string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check ingestion status",
		Long:  "Display the current status of the ingestion pipeline and statistics.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), source, jsonOutput)
		},
	}

	cmd.Flags().StringVarP(&source, "source", "s", "", "Filter by source")
	cmd.Flags().BoolVarP(&jsonOutput, "json", "j", false, "Output in JSON format")

	return cmd
}

// newProcessCmd creates the process subcommand for processing single files.
func newProcessCmd() *cobra.Command {
	var outputDir string
	var skipEmbed bool

	cmd := &cobra.Command{
		Use:   "process [file]",
		Short: "Process a single PDF file",
		Long:  "Process a single PDF file through the ingestion pipeline.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProcess(cmd.Context(), args[0], outputDir, skipEmbed)
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", "./data", "Output directory")
	cmd.Flags().BoolVar(&skipEmbed, "skip-embed", false, "Skip embedding generation")

	return cmd
}

// runIngest executes the ingest command.
func runIngest(ctx context.Context, opts *IngestOptions) error {
	// Setup context with cancellation
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nReceived interrupt signal, cancelling...")
		cancel()
	}()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize logger
	log := logger.New(logger.Config{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
	})
	log.SetDefault()

	log.Info("starting ingestion",
		"source", opts.Source,
		"category", opts.Category,
		"url", opts.URL,
		"input_file", opts.InputFile,
		"dry_run", opts.DryRun,
	)

	// Create orchestrator
	orchestrator, err := NewIngestionOrchestrator(cfg, log, opts.DryRun)
	if err != nil {
		return fmt.Errorf("failed to create orchestrator: %w", err)
	}

	// Run ingestion based on options
	if opts.InputFile != "" {
		return orchestrator.IngestFile(ctx, opts.InputFile, opts)
	} else if opts.URL != "" {
		return orchestrator.IngestURL(ctx, opts.URL, opts)
	} else if opts.Source != "" {
		return orchestrator.IngestSource(ctx, opts)
	}

	return fmt.Errorf("no source, URL, or input file specified")
}

// runEmbed executes the embed command.
func runEmbed(ctx context.Context, source string, force bool, batchSize int) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log := logger.New(logger.Config{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
	})

	log.Info("starting embedding generation", "source", source, "force", force)

	// Create embedder
	emb, err := embedder.NewOpenAIEmbedder(
		embedder.DefaultEmbedderConfig(cfg.LLM.OpenAIKey),
		log,
	)
	if err != nil {
		return fmt.Errorf("failed to create embedder: %w", err)
	}

	// TODO: Load chunks from database and generate embeddings
	// This would need database connection and chunk storage

	log.Info("embedding generation complete")
	stats := emb.GetStats()
	log.Info("embedding stats",
		"total_texts", stats.TotalTexts,
		"total_tokens", stats.TotalTokens,
		"estimated_cost_usd", stats.EstimatedCostUSD,
	)

	return nil
}

// runStatus executes the status command.
func runStatus(ctx context.Context, source string, jsonOutput bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// TODO: Load actual status from database
	status := map[string]interface{}{
		"sources": map[string]interface{}{
			"bnm": map[string]interface{}{
				"documents":  0,
				"chunks":     0,
				"last_crawl": "never",
			},
			"aaoifi": map[string]interface{}{
				"documents":  0,
				"chunks":     0,
				"last_crawl": "never",
			},
		},
		"storage": map[string]interface{}{
			"endpoint": cfg.Storage.Endpoint,
			"bucket":   cfg.Storage.BucketName,
		},
		"embedder": map[string]interface{}{
			"model":     cfg.LLM.EmbeddingModel,
			"dimension": 1536,
		},
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(status, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Println("=== ShariaComply Ingestion Status ===")
		fmt.Println()
		fmt.Println("Sources:")
		fmt.Printf("  BNM:    0 documents, 0 chunks\n")
		fmt.Printf("  AAOIFI: 0 documents, 0 chunks\n")
		fmt.Println()
		fmt.Printf("Storage: %s/%s\n", cfg.Storage.Endpoint, cfg.Storage.BucketName)
		fmt.Printf("Embedder: %s (1536 dimensions)\n", cfg.LLM.EmbeddingModel)
	}

	return nil
}

// runProcess executes the process command for a single file.
func runProcess(ctx context.Context, filePath, outputDir string, skipEmbed bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log := logger.New(logger.Config{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
	})

	log.Info("processing file", "path", filePath)

	// Validate file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", filePath)
	}

	// Create orchestrator
	orchestrator, err := NewIngestionOrchestrator(cfg, log, false)
	if err != nil {
		return fmt.Errorf("failed to create orchestrator: %w", err)
	}

	opts := &IngestOptions{
		OutputDir: outputDir,
		SkipEmbed: skipEmbed,
	}

	return orchestrator.IngestFile(ctx, filePath, opts)
}

// NewIngestionOrchestrator creates a new ingestion orchestrator.
func NewIngestionOrchestrator(cfg *config.Config, log *logger.Logger, dryRun bool) (*IngestionOrchestrator, error) {
	o := &IngestionOrchestrator{
		cfg:   cfg,
		log:   log,
		stats: &IngestionStats{StartTime: time.Now()},
	}

	// Initialize storage (unless dry run)
	if !dryRun {
		store, err := storage.NewMinIOStorage(storage.MinIOConfig{
			Endpoint:        cfg.Storage.Endpoint,
			AccessKeyID:     cfg.Storage.AccessKeyID,
			SecretAccessKey: cfg.Storage.SecretAccessKey,
			BucketName:      cfg.Storage.BucketName,
			UseSSL:          cfg.Storage.UseSSL,
			Region:          cfg.Storage.Region,
		})
		if err != nil {
			log.WithError(err).Warn("failed to initialize storage, continuing without")
		} else {
			o.storage = store
		}
	}

	// Initialize crawler
	crawlerCfg := crawler.DefaultBNMCrawlerConfig()
	crawlerCfg.BaseURL = cfg.Crawler.BNMBaseURL
	crawlerCfg.UserAgent = cfg.Crawler.UserAgent
	crawlerCfg.RateLimit = cfg.Crawler.RateLimit
	o.crawler = crawler.NewBNMCrawler(crawlerCfg, o.storage, log)

	// Initialize PDF processor
	procCfg := processor.DefaultPDFProcessorConfig()
	o.pdfProc = processor.NewPDFProcessor(procCfg, o.storage, log)

	// Initialize chunker
	chunkerCfg := chunker.DefaultChunkerConfig()
	chunkerCfg.MaxTokens = cfg.Crawler.ChunkSize
	chunkerCfg.OverlapTokens = cfg.Crawler.ChunkOverlap
	chunkr, err := chunker.NewSemanticChunker(chunkerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize chunker: %w", err)
	}
	o.chunker = chunkr

	// Initialize embedder (if API key available)
	if cfg.LLM.OpenAIKey != "" {
		embCfg := embedder.DefaultEmbedderConfig(cfg.LLM.OpenAIKey)
		embCfg.Model = cfg.LLM.EmbeddingModel
		emb, err := embedder.NewOpenAIEmbedder(embCfg, log)
		if err != nil {
			log.WithError(err).Warn("failed to initialize embedder, continuing without")
		} else {
			o.embedder = emb
		}
	}

	return o, nil
}

// IngestSource ingests documents from a configured source.
func (o *IngestionOrchestrator) IngestSource(ctx context.Context, opts *IngestOptions) error {
	o.log.Info("ingesting from source", "source", opts.Source, "category", opts.Category)

	switch opts.Source {
	case "bnm":
		return o.ingestBNM(ctx, opts)
	case "aaoifi":
		return o.ingestAAOIFI(ctx, opts)
	case "sc":
		return o.ingestSC(ctx, opts)
	case "iifa":
		return o.ingestIIFA(ctx, opts)
	case "fatwa":
		return o.ingestFatwa(ctx, opts)
	default:
		return fmt.Errorf("unknown source: %s (valid sources: bnm, aaoifi, sc, iifa, fatwa)", opts.Source)
	}
}

// ingestBNM ingests documents from BNM website.
func (o *IngestionOrchestrator) ingestBNM(ctx context.Context, opts *IngestOptions) error {
	// Check if we should use the interactive policy document crawler
	if opts.Category == "policy-documents" || opts.Category == "policy" {
		return o.ingestBNMPolicyDocuments(ctx, opts)
	}

	o.log.Info("crawling BNM website")

	// Crawl BNM pages
	docs, err := o.crawler.Crawl(ctx)
	if err != nil {
		return fmt.Errorf("crawl failed: %w", err)
	}

	atomic.AddInt64(&o.stats.DocumentsFound, int64(len(docs)))
	o.log.Info("crawled documents", "count", len(docs))

	// Create progress bar
	bar := progressbar.NewOptions(len(docs),
		progressbar.OptionSetDescription("Processing documents"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	// Process each document
	for _, doc := range docs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Download and process PDFs
		for _, pdfURL := range doc.PDFLinks {
			if err := o.processPDFFromURL(ctx, pdfURL, doc.Category, opts); err != nil {
				o.log.WithError(err).Error("failed to process PDF", "url", pdfURL)
				atomic.AddInt64(&o.stats.Errors, 1)
				continue
			}
			atomic.AddInt64(&o.stats.PDFsDownloaded, 1)
		}

		// Process HTML content if no PDF
		if len(doc.PDFLinks) == 0 && doc.Content != "" {
			if err := o.processHTMLContent(ctx, doc, opts); err != nil {
				o.log.WithError(err).Error("failed to process HTML content", "url", doc.URL)
				atomic.AddInt64(&o.stats.Errors, 1)
			}
		}

		atomic.AddInt64(&o.stats.DocumentsProcessed, 1)
		bar.Add(1)
	}

	o.printStats()
	return nil
}

// ingestBNMPolicyDocuments uses the interactive crawler to get policy documents.
// This method clicks the "Policy Document" filter checkbox, waits for AJAX update,
// and paginates through all results to extract document information.
func (o *IngestionOrchestrator) ingestBNMPolicyDocuments(ctx context.Context, opts *IngestOptions) error {
	var policyDocs []crawler.PolicyDocument
	var err error

	if opts.UseRScript {
		// Use R script for crawling (more reliable with Cloudflare)
		o.log.Info("crawling BNM policy documents using R script")

		// Get the script path relative to the binary or use absolute path
		scriptPath := filepath.Join("scripts", "bnm_scraper.R")
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			// Try alternative locations
			exePath, _ := os.Executable()
			scriptPath = filepath.Join(filepath.Dir(exePath), "..", "scripts", "bnm_scraper.R")
			if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
				return fmt.Errorf("R script not found. Expected at: scripts/bnm_scraper.R")
			}
		}

		policyDocs, err = o.crawler.CrawlWithRScript(ctx, scriptPath)
		if err != nil {
			return fmt.Errorf("R script crawl failed: %w", err)
		}
	} else {
		// Use Go chromedp crawler
		o.log.Info("crawling BNM policy documents with filter interaction")
		policyDocs, err = o.crawler.CrawlPolicyDocumentsWithFilter(ctx)
		if err != nil {
			return fmt.Errorf("policy documents crawl failed: %w", err)
		}
	}

	atomic.AddInt64(&o.stats.DocumentsFound, int64(len(policyDocs)))
	o.log.Info("found policy documents", "count", len(policyDocs))

	if len(policyDocs) == 0 {
		o.log.Warn("no policy documents found")
		return nil
	}

	// Create progress bar
	bar := progressbar.NewOptions(len(policyDocs),
		progressbar.OptionSetDescription("Downloading & processing policy documents"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	// Process each policy document
	for _, doc := range policyDocs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if doc.PDFLink == "" {
			o.log.Warn("no PDF link for document", "title", doc.Title)
			bar.Add(1)
			continue
		}

		o.log.Info("processing policy document",
			"title", doc.Title,
			"date", doc.Date,
			"type", doc.Type,
			"url", doc.PDFLink,
		)

		// Download and process the PDF
		if err := o.processPDFFromURLWithSource(ctx, doc.PDFLink, "policy-documents", "bnm", opts); err != nil {
			o.log.WithError(err).Error("failed to process policy document PDF",
				"title", doc.Title,
				"url", doc.PDFLink,
			)
			atomic.AddInt64(&o.stats.Errors, 1)
		} else {
			atomic.AddInt64(&o.stats.PDFsDownloaded, 1)
		}

		atomic.AddInt64(&o.stats.DocumentsProcessed, 1)
		bar.Add(1)
	}

	o.printStats()
	return nil
}

// ingestAAOIFI ingests AAOIFI documents (from local PDFs).
func (o *IngestionOrchestrator) ingestAAOIFI(ctx context.Context, opts *IngestOptions) error {
	o.log.Info("processing AAOIFI documents", "input_dir", opts.InputFile)

	// For AAOIFI, we expect a directory of PDFs
	if opts.InputFile == "" {
		opts.InputFile = "./data/aaoifi"
	}

	// Find all PDF files
	var pdfFiles []string
	err := filepath.Walk(opts.InputFile, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".pdf" {
			pdfFiles = append(pdfFiles, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to scan directory: %w", err)
	}

	if len(pdfFiles) == 0 {
		return fmt.Errorf("no PDF files found in %s", opts.InputFile)
	}

	atomic.AddInt64(&o.stats.DocumentsFound, int64(len(pdfFiles)))
	o.log.Info("found PDF files", "count", len(pdfFiles))

	// Process files concurrently
	return o.processFilesParallel(ctx, pdfFiles, "aaoifi", opts)
}

// ingestSC ingests documents from Securities Commission Malaysia website.
func (o *IngestionOrchestrator) ingestSC(ctx context.Context, opts *IngestOptions) error {
	o.log.Info("crawling SC Malaysia website")

	// Create SC crawler
	scCfg := crawler.DefaultSCCrawlerConfig()
	scCfg.BaseURL = o.cfg.Crawler.SCBaseURL
	scCfg.UserAgent = o.cfg.Crawler.UserAgent
	scCrawler := crawler.NewSCCrawler(scCfg, o.storage, o.log)

	// Crawl SC pages
	docs, err := scCrawler.Crawl(ctx)
	if err != nil {
		return fmt.Errorf("SC crawl failed: %w", err)
	}

	atomic.AddInt64(&o.stats.DocumentsFound, int64(len(docs)))
	o.log.Info("crawled SC documents", "count", len(docs))

	// Create progress bar
	bar := progressbar.NewOptions(len(docs),
		progressbar.OptionSetDescription("Processing SC documents"),
		progressbar.OptionShowCount(),
	)

	// Process each document
	for _, doc := range docs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Download and process PDFs
		for _, pdfURL := range doc.PDFLinks {
			if err := o.processPDFFromURLWithSource(ctx, pdfURL, doc.Category, "sc", opts); err != nil {
				o.log.WithError(err).Error("failed to process SC PDF", "url", pdfURL)
				atomic.AddInt64(&o.stats.Errors, 1)
				continue
			}
			atomic.AddInt64(&o.stats.PDFsDownloaded, 1)
		}

		atomic.AddInt64(&o.stats.DocumentsProcessed, 1)
		bar.Add(1)
	}

	o.printStats()
	return nil
}

// ingestIIFA ingests documents from International Islamic Fiqh Academy.
func (o *IngestionOrchestrator) ingestIIFA(ctx context.Context, opts *IngestOptions) error {
	o.log.Info("crawling IIFA (Majma Fiqh) website")

	// Create IIFA crawler
	iifaCfg := crawler.DefaultIIFACrawlerConfig()
	iifaCfg.BaseURL = o.cfg.Crawler.IIFABaseURL
	iifaCfg.UserAgent = o.cfg.Crawler.UserAgent
	iifaCrawler := crawler.NewIIFACrawler(iifaCfg, o.storage, o.log)

	// Crawl IIFA pages
	docs, err := iifaCrawler.Crawl(ctx)
	if err != nil {
		return fmt.Errorf("IIFA crawl failed: %w", err)
	}

	atomic.AddInt64(&o.stats.DocumentsFound, int64(len(docs)))
	o.log.Info("crawled IIFA documents", "count", len(docs))

	// Create progress bar
	bar := progressbar.NewOptions(len(docs),
		progressbar.OptionSetDescription("Processing IIFA documents"),
		progressbar.OptionShowCount(),
	)

	// Process each document
	for _, doc := range docs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Download and process PDFs
		for _, pdfURL := range doc.PDFLinks {
			if err := o.processPDFFromURLWithSource(ctx, pdfURL, doc.Category, "iifa", opts); err != nil {
				o.log.WithError(err).Error("failed to process IIFA PDF", "url", pdfURL)
				atomic.AddInt64(&o.stats.Errors, 1)
				continue
			}
			atomic.AddInt64(&o.stats.PDFsDownloaded, 1)
		}

		atomic.AddInt64(&o.stats.DocumentsProcessed, 1)
		bar.Add(1)
	}

	o.printStats()
	return nil
}

// ingestFatwa ingests documents from Malaysian state fatwa authorities.
func (o *IngestionOrchestrator) ingestFatwa(ctx context.Context, opts *IngestOptions) error {
	// Determine which states to process
	states := getStatesToProcess(opts.State, o.cfg)
	if len(states) == 0 {
		return fmt.Errorf("no fatwa states configured or specified")
	}

	o.log.Info("crawling state fatwa portals", "states", len(states))

	var totalDocs int64
	for _, state := range states {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		baseURL := getFatwaBaseURL(state, o.cfg)
		if baseURL == "" {
			o.log.Warn("no URL configured for state fatwa", "state", state)
			continue
		}

		o.log.Info("crawling state fatwa", "state", state, "url", baseURL)

		// Create fatwa crawler for this state
		fatwaCfg := crawler.DefaultFatwaCrawlerConfig(crawler.FatwaState(state), baseURL)
		fatwaCfg.UserAgent = o.cfg.Crawler.UserAgent
		fatwaCrawler := crawler.NewFatwaCrawler(fatwaCfg, o.storage, o.log)

		// Crawl fatwa pages
		docs, err := fatwaCrawler.Crawl(ctx)
		if err != nil {
			o.log.WithError(err).Error("failed to crawl state fatwa", "state", state)
			atomic.AddInt64(&o.stats.Errors, 1)
			continue
		}

		atomic.AddInt64(&o.stats.DocumentsFound, int64(len(docs)))
		totalDocs += int64(len(docs))

		// Process each document
		for _, doc := range docs {
			// Download and process PDFs
			for _, pdfURL := range doc.PDFLinks {
				sourceType := fmt.Sprintf("fatwa_%s", state)
				if err := o.processPDFFromURLWithSource(ctx, pdfURL, doc.Category, sourceType, opts); err != nil {
					o.log.WithError(err).Error("failed to process fatwa PDF", "state", state, "url", pdfURL)
					atomic.AddInt64(&o.stats.Errors, 1)
					continue
				}
				atomic.AddInt64(&o.stats.PDFsDownloaded, 1)
			}
			atomic.AddInt64(&o.stats.DocumentsProcessed, 1)
		}
	}

	o.log.Info("completed fatwa ingestion", "total_documents", totalDocs)
	o.printStats()
	return nil
}

// getStatesToProcess returns the list of states to process based on the option.
func getStatesToProcess(stateOpt string, cfg *config.Config) []string {
	if stateOpt == "" || stateOpt == "all" {
		// Return all configured states
		var states []string
		if cfg.Crawler.FatwaSelangorURL != "" {
			states = append(states, "selangor")
		}
		if cfg.Crawler.FatwaJohorURL != "" {
			states = append(states, "johor")
		}
		if cfg.Crawler.FatwaPenangURL != "" {
			states = append(states, "penang")
		}
		if cfg.Crawler.FatwaFederalURL != "" {
			states = append(states, "federal")
		}
		if cfg.Crawler.FatwaPerakURL != "" {
			states = append(states, "perak")
		}
		if cfg.Crawler.FatwaKedahURL != "" {
			states = append(states, "kedah")
		}
		if cfg.Crawler.FatwaKelantanURL != "" {
			states = append(states, "kelantan")
		}
		if cfg.Crawler.FatwaTerengganuURL != "" {
			states = append(states, "terengganu")
		}
		if cfg.Crawler.FatwaPahangURL != "" {
			states = append(states, "pahang")
		}
		if cfg.Crawler.FatwaNSembilanURL != "" {
			states = append(states, "nsembilan")
		}
		if cfg.Crawler.FatwaMelakaURL != "" {
			states = append(states, "melaka")
		}
		if cfg.Crawler.FatwaPerlisURL != "" {
			states = append(states, "perlis")
		}
		if cfg.Crawler.FatwaSabahURL != "" {
			states = append(states, "sabah")
		}
		if cfg.Crawler.FatwaSarawakURL != "" {
			states = append(states, "sarawak")
		}
		return states
	}
	return []string{stateOpt}
}

// getFatwaBaseURL returns the base URL for a given state from config.
func getFatwaBaseURL(state string, cfg *config.Config) string {
	switch state {
	case "selangor":
		return cfg.Crawler.FatwaSelangorURL
	case "johor":
		return cfg.Crawler.FatwaJohorURL
	case "penang":
		return cfg.Crawler.FatwaPenangURL
	case "federal":
		return cfg.Crawler.FatwaFederalURL
	case "perak":
		return cfg.Crawler.FatwaPerakURL
	case "kedah":
		return cfg.Crawler.FatwaKedahURL
	case "kelantan":
		return cfg.Crawler.FatwaKelantanURL
	case "terengganu":
		return cfg.Crawler.FatwaTerengganuURL
	case "pahang":
		return cfg.Crawler.FatwaPahangURL
	case "nsembilan":
		return cfg.Crawler.FatwaNSembilanURL
	case "melaka":
		return cfg.Crawler.FatwaMelakaURL
	case "perlis":
		return cfg.Crawler.FatwaPerlisURL
	case "sabah":
		return cfg.Crawler.FatwaSabahURL
	case "sarawak":
		return cfg.Crawler.FatwaSarawakURL
	default:
		return ""
	}
}

// processPDFFromURLWithSource downloads and processes a PDF with a specified source type.
func (o *IngestionOrchestrator) processPDFFromURLWithSource(ctx context.Context, pdfURL, category, sourceType string, opts *IngestOptions) error {
	// Download PDF using the BNM crawler's download method (it's generic enough)
	data, err := o.crawler.DownloadPDF(ctx, pdfURL)
	if err != nil {
		return fmt.Errorf("failed to download PDF: %w", err)
	}

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "shariacomply-*.pdf")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Process the PDF
	metadata := processor.DocumentMetadata{
		ID:         uuid.New().String(),
		SourceType: sourceType,
		Category:   category,
		FileName:   filepath.Base(pdfURL),
	}

	doc, err := o.pdfProc.ProcessPDF(ctx, tmpFile.Name(), metadata)
	if err != nil {
		return fmt.Errorf("failed to process PDF: %w", err)
	}

	atomic.AddInt64(&o.stats.PagesProcessed, int64(doc.TotalPages))

	// Chunk content
	chunks := o.chunkDocument(doc)
	atomic.AddInt64(&o.stats.ChunksCreated, int64(len(chunks)))

	// Generate embeddings
	if !opts.SkipEmbed && o.embedder != nil {
		if err := o.generateEmbeddings(ctx, chunks); err != nil {
			o.log.WithError(err).Warn("failed to generate embeddings")
		} else {
			atomic.AddInt64(&o.stats.EmbeddingsGenerated, int64(len(chunks)))
		}
	}

	return nil
}

// IngestFile ingests a single file.
func (o *IngestionOrchestrator) IngestFile(ctx context.Context, filePath string, opts *IngestOptions) error {
	o.log.Info("processing file", "path", filePath)

	if !processor.IsValidPDF(filePath) {
		return fmt.Errorf("not a valid PDF file: %s", filePath)
	}

	metadata := processor.DocumentMetadata{
		ID:         uuid.New().String(),
		SourceType: "local",
		FileName:   filepath.Base(filePath),
	}

	// Process PDF
	doc, err := o.pdfProc.ProcessPDF(ctx, filePath, metadata)
	if err != nil {
		return fmt.Errorf("failed to process PDF: %w", err)
	}

	atomic.AddInt64(&o.stats.PagesProcessed, int64(doc.TotalPages))

	// Chunk content
	chunks := o.chunkDocument(doc)
	atomic.AddInt64(&o.stats.ChunksCreated, int64(len(chunks)))

	o.log.Info("document processed",
		"pages", doc.TotalPages,
		"chunks", len(chunks),
	)

	// Generate embeddings
	if !opts.SkipEmbed && o.embedder != nil {
		if err := o.generateEmbeddings(ctx, chunks); err != nil {
			o.log.WithError(err).Error("failed to generate embeddings")
		}
	}

	// Save results
	if !opts.DryRun {
		if err := o.saveResults(ctx, doc, chunks, opts.OutputDir); err != nil {
			return fmt.Errorf("failed to save results: %w", err)
		}
	}

	atomic.AddInt64(&o.stats.DocumentsProcessed, 1)
	o.printStats()

	return nil
}

// IngestURL ingests a document from a URL.
func (o *IngestionOrchestrator) IngestURL(ctx context.Context, url string, opts *IngestOptions) error {
	o.log.Info("downloading document", "url", url)

	// Download PDF
	data, err := o.crawler.DownloadPDF(ctx, url)
	if err != nil {
		return fmt.Errorf("failed to download PDF: %w", err)
	}

	atomic.AddInt64(&o.stats.PDFsDownloaded, 1)

	// Process from bytes
	metadata := processor.DocumentMetadata{
		ID:          uuid.New().String(),
		SourceType:  "url",
		OriginalURL: url,
	}

	doc, err := o.pdfProc.ProcessPDFBytes(ctx, data, metadata)
	if err != nil {
		return fmt.Errorf("failed to process PDF: %w", err)
	}

	atomic.AddInt64(&o.stats.PagesProcessed, int64(doc.TotalPages))

	// Chunk and embed
	chunks := o.chunkDocument(doc)
	atomic.AddInt64(&o.stats.ChunksCreated, int64(len(chunks)))

	if !opts.SkipEmbed && o.embedder != nil {
		if err := o.generateEmbeddings(ctx, chunks); err != nil {
			o.log.WithError(err).Error("failed to generate embeddings")
		}
	}

	if !opts.DryRun {
		if err := o.saveResults(ctx, doc, chunks, opts.OutputDir); err != nil {
			return fmt.Errorf("failed to save results: %w", err)
		}
	}

	atomic.AddInt64(&o.stats.DocumentsProcessed, 1)
	o.printStats()

	return nil
}

// processPDFFromURL downloads and processes a PDF from URL.
func (o *IngestionOrchestrator) processPDFFromURL(ctx context.Context, pdfURL, category string, opts *IngestOptions) error {
	// Download PDF
	data, err := o.crawler.DownloadPDF(ctx, pdfURL)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}

	if !processor.IsValidPDFBytes(data) {
		return fmt.Errorf("invalid PDF content")
	}

	// Process
	metadata := processor.DocumentMetadata{
		ID:          uuid.New().String(),
		SourceType:  "bnm",
		OriginalURL: pdfURL,
		Category:    category,
	}

	doc, err := o.pdfProc.ProcessPDFBytes(ctx, data, metadata)
	if err != nil {
		return err
	}

	atomic.AddInt64(&o.stats.PagesProcessed, int64(doc.TotalPages))

	// Chunk
	chunks := o.chunkDocument(doc)
	atomic.AddInt64(&o.stats.ChunksCreated, int64(len(chunks)))

	// Embed
	if !opts.SkipEmbed && o.embedder != nil {
		if err := o.generateEmbeddings(ctx, chunks); err != nil {
			o.log.WithError(err).Warn("embedding failed")
		}
	}

	return nil
}

// processHTMLContent processes HTML content from a crawled page.
func (o *IngestionOrchestrator) processHTMLContent(ctx context.Context, doc crawler.CrawledDocument, opts *IngestOptions) error {
	if doc.Content == "" {
		return nil
	}

	// Create chunk metadata
	metadata := chunker.ChunkMetadata{
		DocumentID:    uuid.New().String(),
		SourceType:    "bnm-html",
		DocumentTitle: doc.Title,
		Extra: map[string]interface{}{
			"url":      doc.URL,
			"category": doc.Category,
		},
	}

	// Chunk the content
	chunks := o.chunker.Chunk(doc.Content, metadata)
	atomic.AddInt64(&o.stats.ChunksCreated, int64(len(chunks)))

	// Embed
	if !opts.SkipEmbed && o.embedder != nil {
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.Content
		}
		if _, err := o.embedder.EmbedBatch(ctx, texts); err != nil {
			o.log.WithError(err).Warn("embedding failed for HTML content")
		} else {
			atomic.AddInt64(&o.stats.EmbeddingsGenerated, int64(len(chunks)))
		}
	}

	return nil
}

// processFilesParallel processes multiple files concurrently.
func (o *IngestionOrchestrator) processFilesParallel(ctx context.Context, files []string, sourceType string, opts *IngestOptions) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.Workers)

	bar := progressbar.NewOptions(len(files),
		progressbar.OptionSetDescription("Processing files"),
		progressbar.OptionShowCount(),
	)

	for _, file := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			metadata := processor.DocumentMetadata{
				ID:         uuid.New().String(),
				SourceType: sourceType,
				FileName:   filepath.Base(f),
			}

			doc, err := o.pdfProc.ProcessPDF(ctx, f, metadata)
			if err != nil {
				o.log.WithError(err).Error("failed to process file", "path", f)
				atomic.AddInt64(&o.stats.Errors, 1)
				bar.Add(1)
				return
			}

			atomic.AddInt64(&o.stats.PagesProcessed, int64(doc.TotalPages))

			chunks := o.chunkDocument(doc)
			atomic.AddInt64(&o.stats.ChunksCreated, int64(len(chunks)))

			if !opts.SkipEmbed && o.embedder != nil {
				if err := o.generateEmbeddings(ctx, chunks); err != nil {
					o.log.WithError(err).Warn("embedding failed", "file", f)
				}
			}

			atomic.AddInt64(&o.stats.DocumentsProcessed, 1)
			bar.Add(1)
		}(file)
	}

	wg.Wait()
	o.printStats()
	return nil
}

// chunkDocument chunks a processed document.
func (o *IngestionOrchestrator) chunkDocument(doc *processor.ProcessedDocument) []chunker.Chunk {
	// Build page content
	pages := make([]chunker.PageContent, len(doc.Pages))
	for i, page := range doc.Pages {
		pages[i] = chunker.PageContent{
			PageNumber: page.PageNumber,
			Text:       page.Text,
		}
	}

	// Create chunk metadata
	metadata := chunker.ChunkMetadata{
		DocumentID:    doc.ID,
		SourceType:    doc.Metadata.SourceType,
		DocumentTitle: doc.Metadata.Title,
	}

	return o.chunker.ChunkPages(pages, metadata)
}

// generateEmbeddings generates embeddings for chunks.
func (o *IngestionOrchestrator) generateEmbeddings(ctx context.Context, chunks []chunker.Chunk) error {
	if o.embedder == nil {
		return nil
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}

	_, err := o.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return err
	}

	atomic.AddInt64(&o.stats.EmbeddingsGenerated, int64(len(chunks)))
	return nil
}

// saveResults saves processing results to disk.
func (o *IngestionOrchestrator) saveResults(ctx context.Context, doc *processor.ProcessedDocument, chunks []chunker.Chunk, outputDir string) error {
	// Create output directory
	docDir := filepath.Join(outputDir, doc.Metadata.SourceType, doc.ID)
	if err := os.MkdirAll(docDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Save document metadata
	metadataPath := filepath.Join(docDir, "metadata.json")
	metadataData, err := json.MarshalIndent(doc.Metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := os.WriteFile(metadataPath, metadataData, 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	// Save chunks
	chunksPath := filepath.Join(docDir, "chunks.json")
	chunksData, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal chunks: %w", err)
	}
	if err := os.WriteFile(chunksPath, chunksData, 0644); err != nil {
		return fmt.Errorf("failed to write chunks: %w", err)
	}

	o.log.Info("results saved", "path", docDir)
	return nil
}

// printStats prints final ingestion statistics.
func (o *IngestionOrchestrator) printStats() {
	o.stats.EndTime = time.Now()
	o.stats.Duration = o.stats.EndTime.Sub(o.stats.StartTime)

	fmt.Println()
	fmt.Println("=== Ingestion Statistics ===")
	fmt.Printf("Duration:            %s\n", o.stats.Duration.Round(time.Second))
	fmt.Printf("Documents Found:     %d\n", o.stats.DocumentsFound)
	fmt.Printf("Documents Processed: %d\n", o.stats.DocumentsProcessed)
	fmt.Printf("PDFs Downloaded:     %d\n", o.stats.PDFsDownloaded)
	fmt.Printf("Pages Processed:     %d\n", o.stats.PagesProcessed)
	fmt.Printf("Chunks Created:      %d\n", o.stats.ChunksCreated)
	fmt.Printf("Embeddings Generated:%d\n", o.stats.EmbeddingsGenerated)
	fmt.Printf("Errors:              %d\n", o.stats.Errors)
	fmt.Println("============================")

	if o.embedder != nil {
		if emb, ok := o.embedder.(*embedder.OpenAIEmbedder); ok {
			stats := emb.GetStats()
			fmt.Printf("\nEmbedding Cost: $%.4f (estimated)\n", stats.EstimatedCostUSD)
		}
	}
}
