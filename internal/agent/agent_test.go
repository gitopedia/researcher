package agent

import (
	"context"
	"testing"

	"github.com/gitopedia/researcher/internal/github"
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/search"
	gh "github.com/google/go-github/v57/github"
)

// Mocks
type MockGitHub struct{}

func (m *MockGitHub) GetResearchRequests() ([]*gh.Issue, error) {
	title := "Article Request: Test Topic"
	number := 1
	return []*gh.Issue{{Title: &title, Number: &number}}, nil
}
func (m *MockGitHub) ListAllOpenIssues() ([]*gh.Issue, error) {
	return []*gh.Issue{}, nil
}
func (m *MockGitHub) CreateBranch(base, new string) error     { return nil }
func (m *MockGitHub) ListBranches() ([]*gh.Branch, error)     { return []*gh.Branch{}, nil }
func (m *MockGitHub) DeleteBranch(branchName string) error    { return nil }
func (m *MockGitHub) CreateFile(b, p, msg, c string) error    { return nil }
func (m *MockGitHub) UpdateFile(b, p, msg, c, s string) error { return nil }
func (m *MockGitHub) CreatePullRequest(t, b, h, base string) (*gh.PullRequest, error) {
	num := 100
	url := "http://pr"
	return &gh.PullRequest{Number: &num, HTMLURL: &url}, nil
}
func (m *MockGitHub) CommentOnIssue(n int, b string) error             { return nil }
func (m *MockGitHub) GetFile(ref, path string) (string, string, error) { return "[]", "sha123", nil }
func (m *MockGitHub) ListAllFiles(path string) ([]string, error)       { return []string{}, nil }
func (m *MockGitHub) CreateIssue(title, body string, labels []string) (*gh.Issue, error) {
	num := 1
	return &gh.Issue{Number: &num}, nil
}

// PR monitoring and management
func (m *MockGitHub) GetPRStatus(prNumber int) (*github.PRStatus, error) {
	return &github.PRStatus{Number: prNumber, State: "open", CIStatus: "success"}, nil
}
func (m *MockGitHub) MergePR(prNumber int, commitMessage string) error { return nil }
func (m *MockGitHub) ClosePR(prNumber int) error                       { return nil }
func (m *MockGitHub) CommentOnPR(prNumber int, body string) error      { return nil }
func (m *MockGitHub) CloseIssue(issueNumber int) error                 { return nil }
func (m *MockGitHub) ReopenIssue(issueNumber int) error                { return nil }
func (m *MockGitHub) ListClosedIssuesWithLabel(label string, limit int) ([]*gh.Issue, error) {
	return nil, nil
}
func (m *MockGitHub) UpdatePRBranch(prNumber int) error                       { return nil }
func (m *MockGitHub) CreateMergeCommitWithResolution(headBranch string) error { return nil }
func (m *MockGitHub) GetFailedCILogs(prNumber int) (string, error)            { return "", nil }
func (m *MockGitHub) ListOpenPRs() ([]*github.PRInfo, error)                  { return nil, nil }
func (m *MockGitHub) ListFilesInBranch(branch, path string) ([]string, error) {
	return []string{}, nil
}
func (m *MockGitHub) DeleteFile(branch, path, message, sha string) error { return nil }
func (m *MockGitHub) MarkPRReady(prNumber int) error                     { return nil }

func (m *MockGitHub) AddLabel(issueNumber int, label string) error    { return nil }
func (m *MockGitHub) RemoveLabel(issueNumber int, label string) error { return nil }
func (m *MockGitHub) HasLabel(issueNumber int, label string) (bool, error) {
	return false, nil
}
func (m *MockGitHub) GetAuthenticatedUsername() (string, error) { return "test-bot", nil }
func (m *MockGitHub) GetIssue(issueNumber int) (*gh.Issue, error) {
	title := "Test Issue"
	botLogin := "test-bot"
	// Simulate successful claim - return issue with bot as sole assignee
	return &gh.Issue{
		Title:     &title,
		Number:    &issueNumber,
		Assignees: []*gh.User{{Login: &botLogin}},
	}, nil
}
func (m *MockGitHub) AddAssignees(issueNumber int, assignees []string) error    { return nil }
func (m *MockGitHub) RemoveAssignees(issueNumber int, assignees []string) error { return nil }
func (m *MockGitHub) IsLocal() bool                                             { return false }
func (m *MockGitHub) GetRepoPath() string                                       { return "" }
func (m *MockGitHub) SetNoCommit(bool)                                          {}

type MockSearch struct{}

func (m *MockSearch) Search(q string) ([]search.Result, error) {
	return []search.Result{{Title: "Res", Body: "Content", Href: "http://example.com/test"}}, nil
}

func (m *MockSearch) FetchContent(url string) (string, error) {
	// Return enough content to pass the 100 character minimum
	return "Mock content from URL that is long enough to pass the minimum length check and provide sufficient test data for the summarization process.", nil
}

type MockLLM struct{}

func (m *MockLLM) GenerateArticle(ctx context.Context, t, c string) (*llm.ArticleResult, error) {
	return &llm.ArticleResult{
		Content:  "# Article\nContent",
		Model:    "mock-model",
		Thinking: "",
	}, nil
}

func (m *MockLLM) AddReferences(ctx context.Context, article, sources string) (string, error) {
	return article, nil // Return article unchanged in mock
}

func (m *MockLLM) ExtractEntities(ctx context.Context, content string) ([]llm.ExtractedEntity, error) {
	return []llm.ExtractedEntity{}, nil
}

func (m *MockLLM) SuggestTopics(ctx context.Context, cat string, exist []string) ([]string, error) {
	return []string{"Topic 1", "Topic 2"}, nil
}

func (m *MockLLM) SummarizeSource(ctx context.Context, topic, urlStr, content string) (llm.SourceSummary, error) {
	// Generate a summary with 400+ words to pass the minimum word count
	longSummary := "This is a comprehensive mock summary that provides detailed information about the topic. " +
		"The content covers multiple aspects including historical context, current applications, and future implications. " +
		"Research has shown that this topic has significant relevance in various fields of study. " +
		"Multiple experts have contributed to the understanding of this subject matter over the years. " +
		"The methodology used in studying this topic involves both qualitative and quantitative approaches. " +
		"Data collection has been extensive, drawing from multiple reliable sources across different regions. " +
		"The analysis reveals several key patterns that are worth noting for further investigation. " +
		"Comparative studies have been conducted to establish benchmarks and baseline measurements. " +
		"The findings suggest that there are both opportunities and challenges in this area. " +
		"Implementation strategies have been developed based on best practices and lessons learned. " +
		"Stakeholder engagement has been crucial in ensuring the success of various initiatives. " +
		"The evaluation framework includes both short-term and long-term performance indicators. " +
		"Continuous improvement processes have been established to maintain quality standards. " +
		"Knowledge transfer mechanisms are in place to ensure sustainability of outcomes. " +
		"The documentation provides comprehensive guidance for practitioners in the field. " +
		"Training programs have been developed to build capacity among key personnel. " +
		"Monitoring systems track progress against established targets and milestones. " +
		"Regular reporting ensures transparency and accountability in all activities. " +
		"Lessons learned are systematically captured and shared across the organization. " +
		"Innovation is encouraged through dedicated research and development efforts. " +
		"Collaboration with external partners enhances the scope and impact of the work. " +
		"Quality assurance mechanisms ensure that all outputs meet required standards. " +
		"Risk management strategies are in place to address potential challenges. " +
		"Resource allocation is optimized to maximize efficiency and effectiveness. " +
		"The conclusion summarizes the key takeaways and recommendations for future action. " +
		"Additionally, the framework incorporates feedback loops that enable iterative improvement. " +
		"External validation has confirmed the reliability and validity of the findings. " +
		"Cross-functional teams work together to address complex multidisciplinary challenges. " +
		"Technology integration has streamlined many previously manual processes significantly. " +
		"The governance structure ensures clear roles, responsibilities, and decision-making authority. " +
		"Communication protocols facilitate effective information sharing among all stakeholders. " +
		"Performance metrics are aligned with strategic objectives and organizational goals. " +
		"Change management approaches help navigate transitions and minimize disruption. " +
		"Capacity building initiatives strengthen institutional capabilities for the long term. " +
		"The sustainability framework addresses environmental, social, and economic dimensions. " +
		"Adaptive management allows for flexibility in responding to changing circumstances. " +
		"Evidence-based decision making is supported by robust data collection and analysis. " +
		"The dissemination strategy ensures findings reach relevant audiences effectively. " +
		"Replication studies have verified the reproducibility of key results consistently. " +
		"The theoretical framework draws on established principles from multiple disciplines. " +
		"Ethical considerations have been carefully addressed throughout the research process. " +
		"The implications for policy and practice are significant and far-reaching indeed."
	return llm.SourceSummary{
		Relevant: true,
		Reason:   "mock",
		Summary:  longSummary,
		Model:    "mock-llm",
	}, nil
}

func (m *MockLLM) CategorizeArticle(ctx context.Context, title string, tags []string, content string, existingCategories []string) (*llm.ArticleCategory, error) {
	return &llm.ArticleCategory{
		Category:  "Science/Biology",
		Reasoning: "Mock categorization",
	}, nil
}

// Incremental workflow methods
func (m *MockLLM) GenerateMiniArticle(ctx context.Context, topic, sourceTitle, sourceSummary string) (string, error) {
	return "## Mini Article\n\nContent about " + topic, nil
}

func (m *MockLLM) CheckRelevance(ctx context.Context, topic, content string) (*llm.RelevanceResult, error) {
	return &llm.RelevanceResult{Relevant: true, Reason: "Mock relevant"}, nil
}

func (m *MockLLM) CheckRedundancy(ctx context.Context, topic, existingArticle, newContent string) (*llm.RedundancyResult, error) {
	return &llm.RedundancyResult{IsRedundant: false, Reason: "Mock unique"}, nil
}

func (m *MockLLM) IntegrateContent(ctx context.Context, topic, existingArticle, newContent string) (string, error) {
	return existingArticle + "\n\n" + newContent, nil
}

func TestAgentRun(t *testing.T) {
	agent := NewAgentWithDeps(&MockGitHub{}, &MockSearch{}, &MockLLM{})
	if err := agent.Run(context.Background(), false, ""); err != nil {
		t.Errorf("Agent.Run failed: %v", err)
	}
}
