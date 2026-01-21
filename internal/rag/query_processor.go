// Package rag provides query preprocessing and expansion for improved retrieval.
package rag

import (
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
)

// QueryIntent represents the classified intent of a query.
type QueryIntent string

const (
	IntentFactual     QueryIntent = "factual"
	IntentComparison  QueryIntent = "comparison"
	IntentCalculation QueryIntent = "calculation"
	IntentLatest      QueryIntent = "latest_updates"
	IntentDefinition  QueryIntent = "definition"
	IntentProcedure   QueryIntent = "procedure"
	IntentCompliance  QueryIntent = "compliance"
	IntentUnknown     QueryIntent = "unknown"
)

// EntityType represents the type of extracted entity.
type EntityType string

const (
	EntityStandardNumber EntityType = "standard_number"
	EntityDate           EntityType = "date"
	EntityTopic          EntityType = "topic"
	EntityOrganization   EntityType = "organization"
	EntityProduct        EntityType = "product"
	EntityRegulation     EntityType = "regulation"
)

// Entity represents an extracted entity from a query.
type Entity struct {
	Type  EntityType `json:"type"`
	Value string     `json:"value"`
	Span  [2]int     `json:"span"` // Start and end position in normalized query
}

// SuggestedFilters represents filters suggested based on query analysis.
type SuggestedFilters struct {
	SourceType     string            `json:"source_type,omitempty"`
	Categories     []string          `json:"categories,omitempty"`
	StandardNumber string            `json:"standard_number,omitempty"`
	DateRange      storage.DateRange `json:"date_range,omitempty"`
}

// ProcessedQuery represents a query after preprocessing.
type ProcessedQuery struct {
	Original      string           `json:"original"`
	Normalized    string           `json:"normalized"`
	Entities      []Entity         `json:"entities"`
	ExpandedTerms []string         `json:"expanded_terms"`
	Intent        QueryIntent      `json:"intent"`
	Filters       SuggestedFilters `json:"filters"`
	Keywords      []string         `json:"keywords"`
	IslamicTerms  []string         `json:"islamic_terms"`
}

// QueryProcessor processes queries for improved retrieval.
type QueryProcessor struct {
	synonymMap      map[string][]string
	topicMap        map[string][]string
	intentPatterns  map[QueryIntent][]*regexp.Regexp
	standardPattern *regexp.Regexp
	datePattern     *regexp.Regexp
	stopWords       map[string]bool
	logger          *slog.Logger
}

// QueryProcessorConfig holds configuration for the query processor.
type QueryProcessorConfig struct {
	EnableSynonymExpansion bool
	EnableEntityExtraction bool
	EnableIntentDetection  bool
	CustomSynonyms         map[string][]string
}

// DefaultQueryProcessorConfig returns default configuration.
func DefaultQueryProcessorConfig() QueryProcessorConfig {
	return QueryProcessorConfig{
		EnableSynonymExpansion: true,
		EnableEntityExtraction: true,
		EnableIntentDetection:  true,
	}
}

// NewQueryProcessor creates a new QueryProcessor instance.
func NewQueryProcessor(logger *slog.Logger, config QueryProcessorConfig) *QueryProcessor {
	if logger == nil {
		logger = slog.Default()
	}

	qp := &QueryProcessor{
		synonymMap:      buildIslamicBankingSynonymMap(),
		topicMap:        buildTopicMap(),
		intentPatterns:  buildIntentPatterns(),
		standardPattern: regexp.MustCompile(`(?i)(?:FAS|SS|AAOIFI|BNM|GP)\s*[\-/]?\s*(\d+)`),
		datePattern:     regexp.MustCompile(`(?i)(\d{1,2}[-/]\d{1,2}[-/]\d{2,4}|\d{4}[-/]\d{1,2}[-/]\d{1,2}|(?:january|february|march|april|may|june|july|august|september|october|november|december)\s+\d{4}|\d{4})`),
		stopWords:       buildStopWords(),
		logger:          logger.With("component", "query_processor"),
	}

	// Merge custom synonyms
	if config.CustomSynonyms != nil {
		for k, v := range config.CustomSynonyms {
			qp.synonymMap[k] = append(qp.synonymMap[k], v...)
		}
	}

	return qp
}

// Process preprocesses a query for improved retrieval.
func (qp *QueryProcessor) Process(query string) *ProcessedQuery {
	qp.logger.Debug("processing query", "original", query)

	result := &ProcessedQuery{
		Original:      query,
		Entities:      []Entity{},
		ExpandedTerms: []string{},
		Keywords:      []string{},
		IslamicTerms:  []string{},
	}

	// 1. Normalize the query
	result.Normalized = qp.normalize(query)

	// 2. Extract entities
	result.Entities = qp.extractEntities(result.Normalized)

	// 3. Extract keywords
	result.Keywords = qp.extractKeywords(result.Normalized)

	// 4. Identify Islamic banking terms
	result.IslamicTerms = qp.identifyIslamicTerms(result.Normalized)

	// 5. Expand query with synonyms
	result.ExpandedTerms = qp.expandQuery(result.Normalized)

	// 6. Classify intent
	result.Intent = qp.classifyIntent(query)

	// 7. Suggest filters based on entities
	result.Filters = qp.suggestFilters(result.Entities, result.Normalized)

	qp.logger.Debug("query processed",
		"normalized", result.Normalized,
		"intent", result.Intent,
		"entities", len(result.Entities),
		"expanded_terms", len(result.ExpandedTerms),
	)

	return result
}

// normalize normalizes the query text.
func (qp *QueryProcessor) normalize(query string) string {
	// Convert to lowercase
	normalized := strings.ToLower(query)

	// Remove excessive whitespace
	normalized = strings.Join(strings.Fields(normalized), " ")

	// Remove special characters but keep alphanumeric, spaces, and hyphens
	re := regexp.MustCompile(`[^\w\s\-/]`)
	normalized = re.ReplaceAllString(normalized, " ")

	// Clean up again
	normalized = strings.Join(strings.Fields(normalized), " ")

	return strings.TrimSpace(normalized)
}

// extractEntities extracts entities from the normalized query.
func (qp *QueryProcessor) extractEntities(query string) []Entity {
	var entities []Entity

	// Extract standard numbers (e.g., "FAS 28", "AAOIFI SS-1", "BNM GP1")
	matches := qp.standardPattern.FindAllStringSubmatchIndex(query, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			entities = append(entities, Entity{
				Type:  EntityStandardNumber,
				Value: strings.ToUpper(query[match[0]:match[1]]),
				Span:  [2]int{match[0], match[1]},
			})
		}
	}

	// Extract dates
	dateMatches := qp.datePattern.FindAllStringSubmatchIndex(query, -1)
	for _, match := range dateMatches {
		if len(match) >= 2 {
			entities = append(entities, Entity{
				Type:  EntityDate,
				Value: query[match[0]:match[1]],
				Span:  [2]int{match[0], match[1]},
			})
		}
	}

	// Extract known topics
	for topic := range qp.topicMap {
		if strings.Contains(query, topic) {
			idx := strings.Index(query, topic)
			entities = append(entities, Entity{
				Type:  EntityTopic,
				Value: topic,
				Span:  [2]int{idx, idx + len(topic)},
			})
		}
	}

	// Extract organization mentions
	orgs := []string{"bnm", "bank negara", "aaoifi", "ifsb", "masb"}
	for _, org := range orgs {
		if strings.Contains(query, org) {
			idx := strings.Index(query, org)
			entities = append(entities, Entity{
				Type:  EntityOrganization,
				Value: strings.ToUpper(org),
				Span:  [2]int{idx, idx + len(org)},
			})
		}
	}

	return entities
}

// extractKeywords extracts important keywords from the query.
func (qp *QueryProcessor) extractKeywords(query string) []string {
	words := strings.Fields(query)
	var keywords []string

	for _, word := range words {
		// Skip stop words
		if qp.stopWords[word] {
			continue
		}
		// Skip very short words
		if len(word) < 3 {
			continue
		}
		keywords = append(keywords, word)
	}

	return keywords
}

// identifyIslamicTerms identifies Islamic banking/finance terms in the query.
func (qp *QueryProcessor) identifyIslamicTerms(query string) []string {
	var terms []string
	words := strings.Fields(query)

	for _, word := range words {
		if _, exists := qp.synonymMap[word]; exists {
			terms = append(terms, word)
		}
	}

	return terms
}

// expandQuery expands the query with synonyms.
func (qp *QueryProcessor) expandQuery(query string) []string {
	var expanded []string
	words := strings.Fields(query)

	for _, word := range words {
		if synonyms, exists := qp.synonymMap[word]; exists {
			expanded = append(expanded, synonyms...)
		}
	}

	// Remove duplicates
	seen := make(map[string]bool)
	var unique []string
	for _, term := range expanded {
		if !seen[term] {
			seen[term] = true
			unique = append(unique, term)
		}
	}

	return unique
}

// classifyIntent classifies the intent of the query.
func (qp *QueryProcessor) classifyIntent(query string) QueryIntent {
	queryLower := strings.ToLower(query)

	// Check each intent pattern
	for intent, patterns := range qp.intentPatterns {
		for _, pattern := range patterns {
			if pattern.MatchString(queryLower) {
				return intent
			}
		}
	}

	return IntentUnknown
}

// suggestFilters suggests search filters based on extracted entities.
func (qp *QueryProcessor) suggestFilters(entities []Entity, query string) SuggestedFilters {
	filters := SuggestedFilters{}

	for _, entity := range entities {
		switch entity.Type {
		case EntityStandardNumber:
			filters.StandardNumber = entity.Value
			// Infer source type from standard number prefix
			valueLower := strings.ToLower(entity.Value)
			if strings.HasPrefix(valueLower, "fas") || strings.HasPrefix(valueLower, "ss") || strings.Contains(valueLower, "aaoifi") {
				filters.SourceType = "aaoifi"
			} else if strings.HasPrefix(valueLower, "bnm") || strings.HasPrefix(valueLower, "gp") {
				filters.SourceType = "bnm"
			}

		case EntityOrganization:
			valueLower := strings.ToLower(entity.Value)
			if strings.Contains(valueLower, "bnm") || strings.Contains(valueLower, "bank negara") {
				filters.SourceType = "bnm"
			} else if strings.Contains(valueLower, "aaoifi") {
				filters.SourceType = "aaoifi"
			}

		case EntityDate:
			// Try to parse and set date range
			if year := qp.extractYear(entity.Value); year > 0 {
				startDate := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
				endDate := time.Date(year, 12, 31, 23, 59, 59, 0, time.UTC)
				filters.DateRange = storage.DateRange{
					Start: startDate,
					End:   endDate,
				}
			}

		case EntityTopic:
			// Map topics to categories
			if categories, exists := qp.topicMap[entity.Value]; exists {
				filters.Categories = append(filters.Categories, categories...)
			}
		}
	}

	// Infer source type from query context if not set
	if filters.SourceType == "" {
		queryLower := strings.ToLower(query)
		if strings.Contains(queryLower, "malaysia") || strings.Contains(queryLower, "bnm") {
			filters.SourceType = "bnm"
		} else if strings.Contains(queryLower, "aaoifi") || strings.Contains(queryLower, "bahrain") {
			filters.SourceType = "aaoifi"
		}
	}

	return filters
}

// extractYear extracts a year from a date string.
func (qp *QueryProcessor) extractYear(dateStr string) int {
	// Try to find a 4-digit year
	yearPattern := regexp.MustCompile(`\b(19|20)\d{2}\b`)
	match := yearPattern.FindString(dateStr)
	if match != "" {
		var year int
		if _, err := regexp.Compile(match); err == nil {
			// Convert string to int
			for _, c := range match {
				year = year*10 + int(c-'0')
			}
			return year
		}
	}
	return 0
}

// GetExpandedSearchQuery returns the query with expanded terms for search.
func (qp *QueryProcessor) GetExpandedSearchQuery(processed *ProcessedQuery) string {
	// Combine original query with expanded terms
	parts := []string{processed.Normalized}
	parts = append(parts, processed.ExpandedTerms...)

	// Remove duplicates and join
	seen := make(map[string]bool)
	var unique []string
	for _, part := range parts {
		if !seen[part] && part != "" {
			seen[part] = true
			unique = append(unique, part)
		}
	}

	return strings.Join(unique, " ")
}

// Helper functions to build maps and patterns

func buildIslamicBankingSynonymMap() map[string][]string {
	return map[string][]string{
		// Arabic terms with English equivalents
		"murabaha":     {"cost-plus", "mark-up financing", "cost-plus financing", "murabahah"},
		"murabahah":    {"cost-plus", "mark-up financing", "murabaha"},
		"musharakah":   {"partnership", "equity sharing", "profit sharing partnership", "musharaka"},
		"musharaka":    {"partnership", "equity sharing", "musharakah"},
		"mudharabah":   {"profit sharing", "trust financing", "mudaraba", "mudarabah"},
		"mudaraba":     {"profit sharing", "trust financing", "mudharabah"},
		"ijarah":       {"leasing", "lease financing", "ijara", "lease"},
		"ijara":        {"leasing", "lease financing", "ijarah"},
		"sukuk":        {"islamic bonds", "islamic securities", "shariah bonds"},
		"takaful":      {"islamic insurance", "shariah insurance", "mutual insurance"},
		"riba":         {"interest", "usury", "prohibited interest"},
		"gharar":       {"uncertainty", "speculation", "excessive risk"},
		"maysir":       {"gambling", "games of chance"},
		"wadiah":       {"safekeeping", "deposit", "custody"},
		"wakalah":      {"agency", "delegation", "wakala"},
		"wakala":       {"agency", "delegation", "wakalah"},
		"qard":         {"loan", "benevolent loan", "qardh"},
		"qardh":        {"loan", "benevolent loan", "qard"},
		"istisna":      {"manufacturing contract", "construction financing", "istisna'a"},
		"salam":        {"forward sale", "advance payment sale"},
		"hibah":        {"gift", "donation"},
		"zakat":        {"alms", "charity tax", "obligatory charity"},
		"waqf":         {"endowment", "charitable trust"},
		"shariah":      {"sharia", "islamic law", "shariah compliant"},
		"sharia":       {"shariah", "islamic law"},
		"halal":        {"permissible", "lawful"},
		"haram":        {"prohibited", "forbidden", "unlawful"},

		// Product terms
		"financing":      {"loan", "credit facility", "funding"},
		"deposit":        {"savings", "investment account"},
		"current account": {"checking account", "demand deposit"},

		// Regulatory terms
		"compliance":     {"regulatory compliance", "shariah compliance"},
		"governance":     {"shariah governance", "corporate governance"},
		"audit":          {"shariah audit", "internal audit"},
		"supervision":    {"regulatory supervision", "prudential supervision"},
	}
}

func buildTopicMap() map[string][]string {
	return map[string][]string{
		"murabaha":     {"islamic_financing", "retail_banking"},
		"musharakah":   {"islamic_financing", "equity_financing"},
		"ijarah":       {"islamic_financing", "leasing"},
		"sukuk":        {"capital_markets", "islamic_securities"},
		"takaful":      {"insurance"},
		"deposit":      {"retail_banking", "deposits"},
		"governance":   {"shariah_governance", "compliance"},
		"audit":        {"shariah_audit", "compliance"},
		"risk":         {"risk_management"},
		"capital":      {"capital_adequacy", "regulatory"},
		"liquidity":    {"liquidity_management", "treasury"},
		"zakat":        {"zakat", "social_finance"},
		"waqf":         {"waqf", "social_finance"},
	}
}

func buildIntentPatterns() map[QueryIntent][]*regexp.Regexp {
	return map[QueryIntent][]*regexp.Regexp{
		IntentFactual: {
			regexp.MustCompile(`(?i)^what\s+(is|are|does)`),
			regexp.MustCompile(`(?i)^who\s+(is|are)`),
			regexp.MustCompile(`(?i)^where\s+(is|are|can)`),
			regexp.MustCompile(`(?i)^when\s+(is|was|did)`),
			regexp.MustCompile(`(?i)tell\s+me\s+about`),
			regexp.MustCompile(`(?i)explain\s+`),
		},
		IntentDefinition: {
			regexp.MustCompile(`(?i)^define\s+`),
			regexp.MustCompile(`(?i)definition\s+of`),
			regexp.MustCompile(`(?i)what\s+is\s+the\s+meaning`),
			regexp.MustCompile(`(?i)what\s+does\s+.+\s+mean`),
		},
		IntentComparison: {
			regexp.MustCompile(`(?i)difference\s+between`),
			regexp.MustCompile(`(?i)compare\s+`),
			regexp.MustCompile(`(?i)versus|vs\.?`),
			regexp.MustCompile(`(?i)which\s+is\s+better`),
			regexp.MustCompile(`(?i)similarities\s+between`),
		},
		IntentCalculation: {
			regexp.MustCompile(`(?i)^calculate\s+`),
			regexp.MustCompile(`(?i)^how\s+much`),
			regexp.MustCompile(`(?i)^what\s+is\s+the\s+(rate|percentage|ratio|amount)`),
			regexp.MustCompile(`(?i)profit\s+rate`),
			regexp.MustCompile(`(?i)formula\s+for`),
		},
		IntentLatest: {
			regexp.MustCompile(`(?i)latest\s+`),
			regexp.MustCompile(`(?i)recent\s+`),
			regexp.MustCompile(`(?i)new\s+(regulation|guideline|standard)`),
			regexp.MustCompile(`(?i)updated?\s+`),
			regexp.MustCompile(`(?i)current\s+`),
			regexp.MustCompile(`(?i)202[0-9]`),
		},
		IntentProcedure: {
			regexp.MustCompile(`(?i)^how\s+(to|do|can)`),
			regexp.MustCompile(`(?i)step.+by.+step`),
			regexp.MustCompile(`(?i)process\s+(for|of)`),
			regexp.MustCompile(`(?i)procedure\s+(for|of)`),
			regexp.MustCompile(`(?i)requirements?\s+(for|to)`),
		},
		IntentCompliance: {
			regexp.MustCompile(`(?i)complian(ce|t)`),
			regexp.MustCompile(`(?i)requirement`),
			regexp.MustCompile(`(?i)regulation`),
			regexp.MustCompile(`(?i)guideline`),
			regexp.MustCompile(`(?i)standard`),
			regexp.MustCompile(`(?i)shariah.+(compliance|requirement)`),
		},
	}
}

func buildStopWords() map[string]bool {
	words := []string{
		"a", "an", "the", "is", "are", "was", "were", "be", "been", "being",
		"have", "has", "had", "do", "does", "did", "will", "would", "could",
		"should", "may", "might", "must", "shall", "can", "need", "dare",
		"ought", "used", "to", "of", "in", "for", "on", "with", "at", "by",
		"from", "as", "into", "through", "during", "before", "after", "above",
		"below", "between", "under", "again", "further", "then", "once",
		"here", "there", "when", "where", "why", "how", "all", "each",
		"few", "more", "most", "other", "some", "such", "no", "nor", "not",
		"only", "own", "same", "so", "than", "too", "very", "just", "and",
		"but", "if", "or", "because", "until", "while", "about", "against",
		"i", "me", "my", "myself", "we", "our", "ours", "ourselves", "you",
		"your", "yours", "yourself", "yourselves", "he", "him", "his",
		"himself", "she", "her", "hers", "herself", "it", "its", "itself",
		"they", "them", "their", "theirs", "themselves", "what", "which",
		"who", "whom", "this", "that", "these", "those", "am",
	}

	stopWords := make(map[string]bool)
	for _, word := range words {
		stopWords[word] = true
	}
	return stopWords
}
