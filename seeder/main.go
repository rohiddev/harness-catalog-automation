package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/template"
)

// ServiceRecord holds data read from CMDB for one repo
type ServiceRecord struct {
	// From CMDB
	SYSID              string // e.g. SYSID-00000
	SYSIDShort         string // e.g. 00000
	PlatformIdentifier string // e.g. payments_platform
	PlatformName       string // e.g. payments-platform
	PlatformDisplay    string // e.g. Payments Platform
	TeamIdentifier     string // e.g. team-payments
	Domain             string // e.g. payments
	JiraProjectKey     string // e.g. PAY
	Language           string // e.g. java

	// From GitHub
	GitHubOrg     string // e.g. bank-org
	RepoName      string // e.g. payments-service
	ServiceDisplay string // e.g. Payments Service

	// Derived
	ServiceIdentifier string // e.g. payments_service (snake_case of RepoName)
	ServiceName       string // e.g. payments-service (same as RepoName)

	// Harness
	AccountID string // Harness account ID
}

// Config holds runtime configuration from environment variables
type Config struct {
	HarnessAPIKey string
	HarnessAccount string
	GitHubPAT     string
	GitHubOrg     string
	DryRun        bool
}

func loadConfig() Config {
	return Config{
		HarnessAPIKey:  os.Getenv("HARNESS_API_KEY"),
		HarnessAccount: os.Getenv("HARNESS_ACCOUNT_ID"),
		GitHubPAT:      os.Getenv("GITHUB_PAT"),
		GitHubOrg:      os.Getenv("GITHUB_ORG"),
		DryRun:         os.Getenv("DRY_RUN") == "true",
	}
}

// renderTemplate fills {{PLACEHOLDER}} values in a template string
func renderTemplate(tmplContent string, record ServiceRecord) (string, error) {
	// Convert {{KEY}} to Go template {{.KEY}} syntax
	content := strings.ReplaceAll(tmplContent, "{{PLATFORM_IDENTIFIER}}", record.PlatformIdentifier)
	content = strings.ReplaceAll(content, "{{PLATFORM_NAME}}", record.PlatformName)
	content = strings.ReplaceAll(content, "{{PLATFORM_DISPLAY_NAME}}", record.PlatformDisplay)
	content = strings.ReplaceAll(content, "{{TEAM_IDENTIFIER}}", record.TeamIdentifier)
	content = strings.ReplaceAll(content, "{{SYSID}}", record.SYSID)
	content = strings.ReplaceAll(content, "{{SYSID_SHORT}}", record.SYSIDShort)
	content = strings.ReplaceAll(content, "{{DOMAIN}}", record.Domain)
	content = strings.ReplaceAll(content, "{{SERVICE_IDENTIFIER}}", record.ServiceIdentifier)
	content = strings.ReplaceAll(content, "{{SERVICE_NAME}}", record.ServiceName)
	content = strings.ReplaceAll(content, "{{SERVICE_DISPLAY_NAME}}", record.ServiceDisplay)
	content = strings.ReplaceAll(content, "{{GITHUB_ORG}}", record.GitHubOrg)
	content = strings.ReplaceAll(content, "{{REPO_NAME}}", record.RepoName)
	content = strings.ReplaceAll(content, "{{ACCOUNT_ID}}", record.AccountID)
	content = strings.ReplaceAll(content, "{{JIRA_PROJECT_KEY}}", record.JiraProjectKey)
	content = strings.ReplaceAll(content, "{{LANGUAGE}}", record.Language)
	return content, nil
}

// toSnakeCase converts kebab-case to snake_case
func toSnakeCase(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

// toKebabCase converts spaces and underscores to kebab-case lowercase
func toKebabCase(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

// seedRepo writes catalog-info.yaml and IDPDoc.md to a local repo clone
func seedRepo(record ServiceRecord, repoPath string, dryRun bool) error {
	catalogTmpl, err := os.ReadFile("seeder/templates/catalog-info.seed.yaml")
	if err != nil {
		return fmt.Errorf("cannot read catalog-info.seed.yaml: %w", err)
	}

	idpDocTmpl, err := os.ReadFile("seeder/templates/IDPDoc.md")
	if err != nil {
		return fmt.Errorf("cannot read IDPDoc.md: %w", err)
	}

	catalogContent, err := renderTemplate(string(catalogTmpl), record)
	if err != nil {
		return fmt.Errorf("render catalog template: %w", err)
	}

	idpDocContent, err := renderTemplate(string(idpDocTmpl), record)
	if err != nil {
		return fmt.Errorf("render IDPDoc template: %w", err)
	}

	if dryRun {
		fmt.Printf("[DRY RUN] Would write catalog-info.yaml to %s\n", repoPath)
		fmt.Printf("[DRY RUN] Would write IDPDoc.md to %s\n", repoPath)
		return nil
	}

	if err := os.WriteFile(repoPath+"/catalog-info.yaml", []byte(catalogContent), 0644); err != nil {
		return fmt.Errorf("write catalog-info.yaml: %w", err)
	}
	if err := os.WriteFile(repoPath+"/IDPDoc.md", []byte(idpDocContent), 0644); err != nil {
		return fmt.Errorf("write IDPDoc.md: %w", err)
	}

	fmt.Printf("seeded %s\n", record.RepoName)
	return nil
}

// registerEntity POSTs a single entity YAML to the Harness Catalog API
func registerEntity(entityYAML string, cfg Config) error {
	if cfg.DryRun {
		fmt.Printf("[DRY RUN] Would register entity\n")
		return nil
	}

	url := fmt.Sprintf(
		"https://app.harness.io/gateway/idp/api/v1/entities?accountIdentifier=%s",
		cfg.HarnessAccount,
	)

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(entityYAML))
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", cfg.HarnessAPIKey)
	req.Header.Set("Content-Type", "application/yaml")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("registration failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

func main() {
	cfg := loadConfig()

	if cfg.HarnessAPIKey == "" || cfg.HarnessAccount == "" {
		fmt.Fprintln(os.Stderr, "HARNESS_API_KEY and HARNESS_ACCOUNT_ID are required")
		os.Exit(1)
	}

	// TODO: replace with CMDB API call
	// For now load from a JSON file for testing
	recordsFile := "seeder/records.json"
	data, err := os.ReadFile(recordsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", recordsFile, err)
		os.Exit(1)
	}

	var records []ServiceRecord
	if err := json.Unmarshal(data, &records); err != nil {
		fmt.Fprintf(os.Stderr, "cannot parse records: %v\n", err)
		os.Exit(1)
	}

	for _, r := range records {
		// Derive fields
		r.ServiceIdentifier = toSnakeCase(r.RepoName)
		r.ServiceName = toKebabCase(r.RepoName)
		r.PlatformIdentifier = toSnakeCase(r.PlatformName)
		r.AccountID = cfg.HarnessAccount

		fmt.Printf("processing %s...\n", r.RepoName)

		// TODO: clone repo, seed files, commit, push
		// For now just print what would happen
		_ = template.Must(template.New("").Parse(""))
		fmt.Printf("  SYSID:    %s\n", r.SYSID)
		fmt.Printf("  Platform: %s\n", r.PlatformIdentifier)
		fmt.Printf("  Team:     %s\n", r.TeamIdentifier)
		fmt.Printf("  Repo:     %s/%s\n", r.GitHubOrg, r.RepoName)
	}
}
