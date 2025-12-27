package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

// Test sources - diverse topics for comprehensive evaluation
var testSources = []struct {
	URL   string
	Topic string
}{
	{"https://en.wikipedia.org/wiki/Photosynthesis", "Photosynthesis"},
	{"https://en.wikipedia.org/wiki/Apollo_11", "Apollo 11"},
	{"https://en.wikipedia.org/wiki/Quantum_computing", "Quantum Computing"},
}

// Models to benchmark (from model-list.txt)
var modelsToTest = []struct {
	Name         string
	ThinkSupport bool
}{
	{"qwen3:8b", true},
	{"qwen3:14b", true},
	{"qwen3:32b", true},
	{"magistral:24b", true},
	{"gpt-oss:20b", true},
	{"deepseek-r1:14b", true},
}

type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaChatMessage    `json:"messages"`
	Stream   bool                   `json:"stream"`
	Think    interface{}            `json:"think,omitempty"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role     string `json:"role"`
		Content  string `json:"content"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"message"`
}

type ollamaShowResponse struct {
	Details struct {
		Family            string   `json:"family"`
		Families          []string `json:"families"`
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
}

type ollamaPsResponse struct {
	Models []struct {
		Name      string `json:"name"`
		SizeVRAM  int64  `json:"size_vram"`
		Size      int64  `json:"size"`
		Processor string `json:"processor"` // "GPU" or "CPU"
	} `json:"models"`
}

// SourceFacts holds extracted ground truth facts from a source
type SourceFacts struct {
	Topic    string
	URL      string
	RawText  string
	Facts    []string // Key facts extracted for comparison
	WordCount int
}

// BenchmarkResult holds results for one model on one source
type BenchmarkResult struct {
	Model         string
	Source        string
	Topic         string
	Duration      time.Duration
	WordCount     int
	CharCount     int
	FactsCovered  int
	FactsTotal    int
	FactScore     float64 // FactsCovered / FactsTotal
	Processor     string  // GPU or CPU
	ThinkingUsed  bool
	ThinkingLen   int
	Summary       string
	Error         string
}

// SystemPrompt for summarization (same as production)
const systemPrompt = `You extract informative content from web pages for encyclopedia articles.

Your task is to EXTRACT (not summarize) all useful factual content from the source.

OUTPUT FORMAT:
Use the same section structure as the source document. For each section:
# Section Heading
- Specific fact with concrete detail
- Another fact (include any names, dates, numbers, places)
- Continue for all facts in that section

EXTRACTION RULES:
- Follow the source's own organization - use its headings and structure
- Include ALL facts: names, dates, numbers, statistics, places, events
- Preserve specific details - do not generalize or round numbers
- Each bullet should be a complete, informative statement
- Cover every section present in the source

LENGTH REQUIREMENT:
Your output should be proportional to the source length. For comprehensive sources, output 1500-3000 words. Do not artificially compress.

OUTPUT ONLY the extracted content. Do not add:
- Introductions or conclusions you invented
- Conversational phrases ("Let me know...", "I hope this helps...")
- Meta-commentary about the extraction process`

func fetchPageContent(url string) (string, error) {
	log.Printf("  Fetching %s...", url)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	ctx, cancelTimeout := context.WithTimeout(ctx, 60*time.Second)
	defer cancelTimeout()

	var htmlContent string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(3*time.Second),
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return "", err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return "", err
	}

	// Remove non-content sections
	doc.Find("#References, #Notes, #Citations, #External_links, #See_also, #Further_reading").Each(func(i int, s *goquery.Selection) {
		s.Parent().NextUntil("h2").Remove()
		s.Parent().Remove()
	})
	doc.Find(".reflist, .references, .citation, .navbox, .sidebar, .infobox, .toc, #toc, .mw-editsection").Remove()

	var textBuilder strings.Builder

	selectors := []string{".mw-parser-output", "#mw-content-text", "#bodyContent", "article", "main"}
	for _, sel := range selectors {
		container := doc.Find(sel).First()
		if container.Length() > 0 {
			container.Find("h1, h2, h3, h4, p, li").Each(func(i int, s *goquery.Selection) {
				if s.ParentsFiltered(".reflist, .references, .navbox, .sidebar, nav, footer, .toc").Length() > 0 {
					return
				}
				text := strings.Join(strings.Fields(s.Text()), " ")
				if len(text) > 20 {
					textBuilder.WriteString(text + "\n\n")
				}
			})
			if textBuilder.Len() > 0 {
				break
			}
		}
	}

	return textBuilder.String(), nil
}

// extractKeyFacts extracts key factual statements from source content
// These will be used to score how well models capture important information
func extractKeyFacts(content, topic string) []string {
	var facts []string

	// Patterns to identify factual statements
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\b(\d{4})\b`),                                      // Years
		regexp.MustCompile(`\b(\d+(?:\.\d+)?)\s*(?:percent|%|million|billion|kg|km|meters?|miles?|hours?|days?|years?)`), // Numbers with units
		regexp.MustCompile(`\b[A-Z][a-z]+\s+[A-Z][a-z]+\b`),                     // Proper names
		regexp.MustCompile(`(?i)(?:discovered|invented|founded|created|established)\s+(?:by|in)\s+\d{4}`),
		regexp.MustCompile(`(?i)(?:first|largest|smallest|oldest|youngest|fastest)`),
	}

	// Split into sentences
	sentences := regexp.MustCompile(`[.!?]+\s+`).Split(content, -1)

	seen := make(map[string]bool)
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if len(sentence) < 30 || len(sentence) > 500 {
			continue
		}

		// Check if sentence contains factual indicators
		factual := false
		for _, pattern := range patterns {
			if pattern.MatchString(sentence) {
				factual = true
				break
			}
		}

		// Also include sentences with key topic words
		topicWords := strings.Fields(strings.ToLower(topic))
		for _, tw := range topicWords {
			if strings.Contains(strings.ToLower(sentence), tw) {
				factual = true
				break
			}
		}

		if factual {
			// Normalize for deduplication
			normalized := strings.ToLower(strings.Join(strings.Fields(sentence), " "))
			if !seen[normalized] && len(normalized) > 30 {
				seen[normalized] = true
				facts = append(facts, sentence)
			}
		}
	}

	// Limit to most important facts (first 50)
	if len(facts) > 50 {
		facts = facts[:50]
	}

	return facts
}

// scoreFacts checks how many ground truth facts appear in the summary
func scoreFacts(summary string, facts []string) (covered int, total int) {
	summaryLower := strings.ToLower(summary)
	total = len(facts)

	for _, fact := range facts {
		// Check for key phrases from the fact
		factLower := strings.ToLower(fact)

		// Extract key terms (numbers, proper nouns, key phrases)
		terms := extractKeyTerms(factLower)

		// A fact is "covered" if at least 60% of its key terms appear in summary
		matchedTerms := 0
		for _, term := range terms {
			if strings.Contains(summaryLower, term) {
				matchedTerms++
			}
		}

		if len(terms) > 0 && float64(matchedTerms)/float64(len(terms)) >= 0.6 {
			covered++
		}
	}

	return covered, total
}

// extractKeyTerms pulls out important terms from a fact for matching
func extractKeyTerms(fact string) []string {
	var terms []string

	// Numbers (years, quantities)
	numPattern := regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	terms = append(terms, numPattern.FindAllString(fact, -1)...)

	// Multi-word phrases (2-3 consecutive words that might be names/concepts)
	words := strings.Fields(fact)
	for i := 0; i < len(words)-1; i++ {
		if len(words[i]) > 3 && len(words[i+1]) > 3 {
			terms = append(terms, words[i]+" "+words[i+1])
		}
	}

	return terms
}

// checkProcessorUsage queries Ollama to see if model is on GPU or CPU
func checkProcessorUsage(model string) string {
	resp, err := http.Get("http://localhost:11434/api/ps")
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close()

	var psResp ollamaPsResponse
	if err := json.NewDecoder(resp.Body).Decode(&psResp); err != nil {
		return "unknown"
	}

	for _, m := range psResp.Models {
		if strings.HasPrefix(m.Name, strings.Split(model, ":")[0]) {
			// Check VRAM usage - if size_vram > 0, it's on GPU
			if m.SizeVRAM > 0 {
				vramGB := float64(m.SizeVRAM) / (1024 * 1024 * 1024)
				return fmt.Sprintf("GPU (%.1fGB VRAM)", vramGB)
			}
			return "CPU"
		}
	}

	return "unknown"
}

// preloadModel loads the model into memory before benchmarking
func preloadModel(model string) error {
	log.Printf("  Pre-loading model %s...", model)

	req := ollamaChatRequest{
		Model: model,
		Messages: []ollamaChatMessage{
			{Role: "user", Content: "Hello"},
		},
		Stream: false,
		Options: map[string]interface{}{
			"num_predict": 1,
		},
	}

	reqBody, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", "http://localhost:11434/api/chat", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	// Give it a moment to fully load
	time.Sleep(2 * time.Second)
	return nil
}

// runSummarization runs the summarization task and returns results
func runSummarization(model string, source SourceFacts, useThinking bool) BenchmarkResult {
	result := BenchmarkResult{
		Model:        model,
		Source:       source.URL,
		Topic:        source.Topic,
		ThinkingUsed: useThinking,
	}

	// Build user prompt
	userPrompt := fmt.Sprintf(`Extract all factual content about "%s" from this web page.

Output "NOT_RELEVANT" only if the page has no useful content.

Otherwise, extract comprehensively using heading + bullet format.

Page URL: %s

Page Content:
%s`, source.Topic, source.URL, source.RawText)

	// Build request
	options := map[string]interface{}{
		"temperature": 0.3,
		"num_predict": 16000,
	}

	var thinkParam interface{} = false
	if useThinking {
		thinkParam = true
	}

	req := ollamaChatRequest{
		Model: model,
		Messages: []ollamaChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:  false,
		Think:   thinkParam,
		Options: options,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		result.Error = fmt.Sprintf("marshal error: %v", err)
		return result
	}

	// Check processor before running
	processor := checkProcessorUsage(model)

	// Run the request
	start := time.Now()
	httpReq, _ := http.NewRequest("POST", "http://localhost:11434/api/chat", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		result.Error = fmt.Sprintf("request error: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	result.Duration = time.Since(start)

	if err != nil {
		result.Error = fmt.Sprintf("read error: %v", err)
		return result
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		result.Error = fmt.Sprintf("parse error: %v", err)
		return result
	}

	// Check processor after running (should be warm now)
	result.Processor = checkProcessorUsage(model)
	if result.Processor == "unknown" {
		result.Processor = processor
	}

	result.Summary = chatResp.Message.Content
	result.WordCount = len(strings.Fields(result.Summary))
	result.CharCount = len(result.Summary)
	result.ThinkingLen = len(chatResp.Message.Thinking)

	// Score fact coverage
	result.FactsCovered, result.FactsTotal = scoreFacts(result.Summary, source.Facts)
	if result.FactsTotal > 0 {
		result.FactScore = float64(result.FactsCovered) / float64(result.FactsTotal)
	}

	return result
}

func main() {
	log.Println("╔══════════════════════════════════════════════════════════════╗")
	log.Println("║     GITOPEDIA RESEARCHER - MODEL SUMMARIZATION BENCHMARK     ║")
	log.Println("╚══════════════════════════════════════════════════════════════╝")
	log.Println()

	// Create results directory
	os.MkdirAll("experiments/benchmark_results", 0755)

	// Phase 1: Fetch all sources and extract ground truth facts
	log.Println("=== PHASE 1: Fetching Sources & Extracting Ground Truth Facts ===")
	var sources []SourceFacts

	for _, src := range testSources {
		log.Printf("\nProcessing: %s", src.Topic)
		content, err := fetchPageContent(src.URL)
		if err != nil {
			log.Printf("  ERROR fetching: %v", err)
			continue
		}

		wordCount := len(strings.Fields(content))
		log.Printf("  Fetched %d chars (%d words)", len(content), wordCount)

		facts := extractKeyFacts(content, src.Topic)
		log.Printf("  Extracted %d key facts for scoring", len(facts))

		sources = append(sources, SourceFacts{
			Topic:     src.Topic,
			URL:       src.URL,
			RawText:   content,
			Facts:     facts,
			WordCount: wordCount,
		})

		// Save source facts for reference
		factsFile := fmt.Sprintf("experiments/benchmark_results/facts_%s.txt", 
			strings.ReplaceAll(strings.ToLower(src.Topic), " ", "_"))
		var factsContent strings.Builder
		factsContent.WriteString(fmt.Sprintf("# Ground Truth Facts: %s\n", src.Topic))
		factsContent.WriteString(fmt.Sprintf("Source: %s\n", src.URL))
		factsContent.WriteString(fmt.Sprintf("Total facts extracted: %d\n\n", len(facts)))
		for i, fact := range facts {
			factsContent.WriteString(fmt.Sprintf("%d. %s\n", i+1, fact))
		}
		os.WriteFile(factsFile, []byte(factsContent.String()), 0644)
	}

	if len(sources) == 0 {
		log.Fatal("No sources could be fetched!")
	}

	// Phase 2: Run benchmarks
	log.Println("\n=== PHASE 2: Running Model Benchmarks ===")
	log.Printf("Testing %d models × %d sources = %d runs\n", 
		len(modelsToTest), len(sources), len(modelsToTest)*len(sources))

	var allResults []BenchmarkResult

	for _, model := range modelsToTest {
		log.Printf("\n┌─────────────────────────────────────────────────────────────┐")
		log.Printf("│ MODEL: %-53s │", model.Name)
		log.Printf("└─────────────────────────────────────────────────────────────┘")

		// Preload model
		if err := preloadModel(model.Name); err != nil {
			log.Printf("  WARNING: Could not preload model: %v", err)
		}

		for _, source := range sources {
			log.Printf("\n  Testing on: %s", source.Topic)
			log.Printf("    Source: %d words, %d facts to score", source.WordCount, len(source.Facts))

			// Run with thinking enabled (for models that support it)
			result := runSummarization(model.Name, source, model.ThinkSupport)

			if result.Error != "" {
				log.Printf("    ERROR: %s", result.Error)
			} else {
				log.Printf("    Result: %d words, %.1f%% facts covered, %v, %s",
					result.WordCount,
					result.FactScore*100,
					result.Duration.Round(time.Second),
					result.Processor)
				if result.ThinkingLen > 0 {
					log.Printf("    Thinking: %d chars", result.ThinkingLen)
				}
			}

			allResults = append(allResults, result)

			// Save individual result
			if result.Error == "" {
				filename := fmt.Sprintf("experiments/benchmark_results/%s_%s.md",
					strings.ReplaceAll(model.Name, ":", "_"),
					strings.ReplaceAll(strings.ToLower(source.Topic), " ", "_"))
				os.WriteFile(filename, []byte(result.Summary), 0644)
			}
		}
	}

	// Phase 3: Generate comprehensive report
	log.Println("\n=== PHASE 3: Generating Report ===")

	// Sort results by fact score (descending)
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].FactScore > allResults[j].FactScore
	})

	// Generate markdown report
	var report strings.Builder
	report.WriteString("# Model Summarization Benchmark Results\n\n")
	report.WriteString(fmt.Sprintf("**Generated:** %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
	report.WriteString(fmt.Sprintf("**Sources tested:** %d\n", len(sources)))
	report.WriteString(fmt.Sprintf("**Models tested:** %d\n\n", len(modelsToTest)))

	// Sources info
	report.WriteString("## Test Sources\n\n")
	report.WriteString("| Topic | URL | Words | Facts |\n")
	report.WriteString("|-------|-----|------:|------:|\n")
	for _, src := range sources {
		report.WriteString(fmt.Sprintf("| %s | %s | %d | %d |\n",
			src.Topic, src.URL, src.WordCount, len(src.Facts)))
	}

	// Aggregate results by model
	report.WriteString("\n## Results by Model (Averaged)\n\n")
	report.WriteString("| Model | Avg Words | Avg Fact Score | Avg Time | Processor |\n")
	report.WriteString("|-------|----------:|---------------:|---------:|----------:|\n")

	modelStats := make(map[string]struct {
		TotalWords    int
		TotalFacts    float64
		TotalDuration time.Duration
		Count         int
		Processor     string
		Errors        int
	})

	for _, r := range allResults {
		stats := modelStats[r.Model]
		if r.Error == "" {
			stats.TotalWords += r.WordCount
			stats.TotalFacts += r.FactScore
			stats.TotalDuration += r.Duration
			stats.Count++
			stats.Processor = r.Processor
		} else {
			stats.Errors++
		}
		modelStats[r.Model] = stats
	}

	// Sort models by fact score
	var modelNames []string
	for name := range modelStats {
		modelNames = append(modelNames, name)
	}
	sort.Slice(modelNames, func(i, j int) bool {
		si, sj := modelStats[modelNames[i]], modelStats[modelNames[j]]
		if si.Count == 0 {
			return false
		}
		if sj.Count == 0 {
			return true
		}
		return si.TotalFacts/float64(si.Count) > sj.TotalFacts/float64(sj.Count)
	})

	for _, name := range modelNames {
		stats := modelStats[name]
		if stats.Count == 0 {
			report.WriteString(fmt.Sprintf("| %s | - | - | - | %d errors |\n", name, stats.Errors))
			continue
		}
		avgWords := stats.TotalWords / stats.Count
		avgFacts := stats.TotalFacts / float64(stats.Count) * 100
		avgTime := stats.TotalDuration / time.Duration(stats.Count)
		report.WriteString(fmt.Sprintf("| **%s** | %d | %.1f%% | %v | %s |\n",
			name, avgWords, avgFacts, avgTime.Round(time.Second), stats.Processor))
	}

	// Detailed results
	report.WriteString("\n## Detailed Results\n\n")
	report.WriteString("| Model | Topic | Words | Facts Covered | Score | Time | Processor |\n")
	report.WriteString("|-------|-------|------:|--------------:|------:|-----:|----------:|\n")

	for _, r := range allResults {
		if r.Error != "" {
			report.WriteString(fmt.Sprintf("| %s | %s | ERROR | - | - | - | %s |\n",
				r.Model, r.Topic, r.Error))
			continue
		}
		report.WriteString(fmt.Sprintf("| %s | %s | %d | %d/%d | %.1f%% | %v | %s |\n",
			r.Model, r.Topic, r.WordCount, r.FactsCovered, r.FactsTotal,
			r.FactScore*100, r.Duration.Round(time.Second), r.Processor))
	}

	// Recommendations
	report.WriteString("\n## Recommendations\n\n")
	if len(modelNames) > 0 && modelStats[modelNames[0]].Count > 0 {
		bestModel := modelNames[0]
		stats := modelStats[bestModel]
		avgFacts := stats.TotalFacts / float64(stats.Count) * 100
		avgTime := stats.TotalDuration / time.Duration(stats.Count)
		report.WriteString(fmt.Sprintf("**Best Overall:** `%s` with %.1f%% fact coverage, avg %v per source\n\n",
			bestModel, avgFacts, avgTime.Round(time.Second)))
	}

	// Check for GPU/CPU issues
	cpuModels := []string{}
	for _, r := range allResults {
		if strings.Contains(r.Processor, "CPU") {
			cpuModels = append(cpuModels, r.Model)
		}
	}
	if len(cpuModels) > 0 {
		report.WriteString("⚠️ **Warning:** The following models ran on CPU (much slower):\n")
		seen := make(map[string]bool)
		for _, m := range cpuModels {
			if !seen[m] {
				report.WriteString(fmt.Sprintf("- `%s`\n", m))
				seen[m] = true
			}
		}
	}

	// Save report
	reportPath := "experiments/benchmark_results/BENCHMARK_REPORT.md"
	os.WriteFile(reportPath, []byte(report.String()), 0644)

	// Save raw JSON results
	jsonResults, _ := json.MarshalIndent(allResults, "", "  ")
	os.WriteFile("experiments/benchmark_results/results.json", jsonResults, 0644)

	log.Println("\n" + strings.Repeat("═", 65))
	log.Println("BENCHMARK COMPLETE!")
	log.Println(strings.Repeat("═", 65))
	log.Printf("Report saved to: %s", reportPath)
	log.Printf("Individual summaries saved to: experiments/benchmark_results/")

	// Print quick summary
	fmt.Println("\n=== QUICK SUMMARY ===")
	fmt.Printf("%-20s %8s %10s %12s %s\n", "Model", "Words", "FactScore", "Time", "Processor")
	fmt.Println(strings.Repeat("-", 70))
	for _, name := range modelNames {
		stats := modelStats[name]
		if stats.Count == 0 {
			fmt.Printf("%-20s %8s %10s %12s %s\n", name, "-", "-", "-", "errors")
			continue
		}
		avgWords := stats.TotalWords / stats.Count
		avgFacts := stats.TotalFacts / float64(stats.Count) * 100
		avgTime := stats.TotalDuration / time.Duration(stats.Count)
		fmt.Printf("%-20s %8d %9.1f%% %12v %s\n",
			name, avgWords, avgFacts, avgTime.Round(time.Second), stats.Processor)
	}
}

