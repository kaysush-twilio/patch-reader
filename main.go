package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kaysush-twilio/patch-reader/internal/avro"
)

const (
	maxN        = 1000
	concurrency = 100
)

type Result struct {
	N        int
	PK       string
	SK       string
	AvroData []byte
	Patch    *avro.ProfileIdentifierPatch
}

type QueryJob struct {
	N  int
	PK string
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			MarginBottom(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Bold(true)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	jsonStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("115"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)
)

// TUI Model
type model struct {
	results      []*Result
	cursor       int
	scrollOffset int
	viewHeight   int
	jsonScroll   int
	jsonLines    []string
	quitting     bool
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.jsonScroll = 0
				m.updateJSON()
				if m.cursor < m.scrollOffset {
					m.scrollOffset = m.cursor
				}
			}
		case "down", "j":
			if m.cursor < len(m.results)-1 {
				m.cursor++
				m.jsonScroll = 0
				m.updateJSON()
				if m.cursor >= m.scrollOffset+m.viewHeight {
					m.scrollOffset = m.cursor - m.viewHeight + 1
				}
			}
		case "pgup":
			m.jsonScroll -= 10
			if m.jsonScroll < 0 {
				m.jsonScroll = 0
			}
		case "pgdown":
			m.jsonScroll += 10
			maxScroll := len(m.jsonLines) - 20
			if maxScroll < 0 {
				maxScroll = 0
			}
			if m.jsonScroll > maxScroll {
				m.jsonScroll = maxScroll
			}
		case "home":
			m.cursor = 0
			m.scrollOffset = 0
			m.jsonScroll = 0
			m.updateJSON()
		case "end":
			m.cursor = len(m.results) - 1
			m.jsonScroll = 0
			m.updateJSON()
			if m.cursor >= m.viewHeight {
				m.scrollOffset = m.cursor - m.viewHeight + 1
			}
		case "enter":
			// Output current selection and exit
			m.quitting = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.viewHeight = min(10, len(m.results))
	}
	return m, nil
}

func (m *model) updateJSON() {
	if m.cursor >= 0 && m.cursor < len(m.results) {
		result := m.results[m.cursor]
		if result.Patch != nil {
			jsonData, _ := json.MarshalIndent(result.Patch, "", "  ")
			m.jsonLines = strings.Split(string(jsonData), "\n")
		} else {
			m.jsonLines = []string{"(Unable to deserialize)"}
		}
	}
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render(fmt.Sprintf("Patches (%d found) - Navigate: ↑/↓  Scroll JSON: PgUp/PgDn  Select: Enter  Quit: q/Ctrl+C", len(m.results))))
	b.WriteString("\n\n")

	// Patch list
	listLines := []string{}
	for i, r := range m.results {
		if i < m.scrollOffset || i >= m.scrollOffset+m.viewHeight {
			continue
		}

		line := formatPatchLine(r)
		if i == m.cursor {
			listLines = append(listLines, selectedStyle.Render("▸ "+line))
		} else {
			listLines = append(listLines, normalStyle.Render("  "+line))
		}
	}
	b.WriteString(strings.Join(listLines, "\n"))

	// Scroll indicator
	if len(m.results) > m.viewHeight {
		b.WriteString(dimStyle.Render(fmt.Sprintf("\n  ... showing %d-%d of %d", m.scrollOffset+1, min(m.scrollOffset+m.viewHeight, len(m.results)), len(m.results))))
	}

	b.WriteString("\n\n")

	// JSON preview
	b.WriteString(dimStyle.Render("─── JSON Preview ───────────────────────────────────────────────────────────────"))
	b.WriteString("\n")

	// Show JSON with scroll
	jsonViewHeight := 25
	endLine := min(m.jsonScroll+jsonViewHeight, len(m.jsonLines))
	visibleLines := m.jsonLines[m.jsonScroll:endLine]

	for _, line := range visibleLines {
		b.WriteString(jsonStyle.Render(line))
		b.WriteString("\n")
	}

	// JSON scroll indicator
	if len(m.jsonLines) > jsonViewHeight {
		b.WriteString(dimStyle.Render(fmt.Sprintf("... lines %d-%d of %d (PgUp/PgDn to scroll)", m.jsonScroll+1, endLine, len(m.jsonLines))))
	}

	return b.String()
}

func formatPatchLine(r *Result) string {
	if r.Patch != nil {
		ts := formatTimestamp(r.Patch.AcceptedAt)
		event := r.Patch.Event.Name
		if event == "" {
			event = "unknown"
		}
		if len(event) > 25 {
			event = event[:22] + "..."
		}
		summary := string(r.Patch.Summary)
		return fmt.Sprintf("%-45s │ %s │ %-25s │ %s", r.SK, ts, event, summary)
	}
	return r.SK
}

func formatTimestamp(ts int64) string {
	if ts == 0 {
		return "N/A                "
	}
	t := time.UnixMilli(ts)
	return t.Format("2006-01-02 15:04:05")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// validateAWSCredentials checks if AWS credentials are valid and provides helpful error messages
func validateAWSCredentials(ctx context.Context, cfg aws.Config, profile string) error {
	stsClient := sts.NewFromConfig(cfg)

	_, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return formatAWSError(err, profile)
	}

	return nil
}

// formatAWSError provides user-friendly error messages for common AWS errors
func formatAWSError(err error, profile string) error {
	errStr := err.Error()

	// Check for common error patterns
	switch {
	case strings.Contains(errStr, "could not find profile") || strings.Contains(errStr, "failed to get shared config profile"):
		return fmt.Errorf(`AWS profile "%s" not found

To fix this, add the profile to ~/.aws/config:

  [profile %s]
  sso_session = twilio
  sso_account_id = <ACCOUNT_ID>
  sso_role_name = Standard_PowerUser
  region = us-east-1

Then run: aws sso login --sso-session twilio`, profile, profile)

	case strings.Contains(errStr, "Token has expired"):
		return fmt.Errorf(`AWS SSO token has expired

To fix this, run:
  aws sso login --sso-session twilio`)

	case strings.Contains(errStr, "refresh failed"):
		return fmt.Errorf(`AWS SSO token refresh failed

To fix this, run:
  aws sso login --sso-session twilio`)

	case strings.Contains(errStr, "InvalidGrantException") ||
		strings.Contains(errStr, "refresh cached SSO token failed"):
		return fmt.Errorf(`AWS SSO token is invalid or expired

To fix this, run:
  aws sso login --sso-session twilio`)

	case strings.Contains(errStr, "no EC2 IMDS role found") ||
		strings.Contains(errStr, "failed to refresh cached credentials") ||
		strings.Contains(errStr, "failed to retrieve credentials"):
		return fmt.Errorf(`No valid AWS credentials found

To fix this, login via SSO:
  aws sso login --sso-session twilio`)

	case strings.Contains(errStr, "AccessDenied") || strings.Contains(errStr, "not authorized"):
		return fmt.Errorf(`Access denied - your AWS profile "%s" doesn't have permission

Check that your profile has access to the DynamoDB IdentityPatch tables.
Contact your admin if you need access.`, profile)

	case strings.Contains(errStr, "ResourceNotFoundException"):
		return fmt.Errorf(`DynamoDB table not found

The table may not exist in this environment/region. Check:
  - Environment (-env): dev, stage, prod
  - Region (-region): us-east-1, etc.
  - Cell (-cell): cell-1, etc.`)

	case strings.Contains(errStr, "UnrecognizedClientException"):
		return fmt.Errorf(`Invalid AWS credentials

Your credentials may be malformed or from the wrong account.
Try re-authenticating: aws sso login --sso-session twilio`)

	default:
		return fmt.Errorf("AWS error: %v", err)
	}
}

func main() {
	// CLI flags
	profileID := flag.String("profile-id", "", "Profile ID (required)")
	storeID := flag.String("store-id", "", "Store ID (required)")
	patchKey := flag.String("patch-key", "", "Patch Key / SK (optional - if omitted, shows all patches)")
	env := flag.String("env", "dev", "Environment: dev, stage, prod")
	region := flag.String("region", "us-east-1", "AWS region")
	cell := flag.String("cell", "cell-1", "Cell identifier")
	raw := flag.Bool("raw", false, "Output raw Avro bytes (base64) instead of JSON")
	all := flag.Bool("all", false, "Output all matches as JSON array (no interactive selection)")
	awsProfile := flag.String("aws-profile", "", "AWS profile to use (overrides AWS_PROFILE env var)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "patch-reader - Query DynamoDB IdentityPatch table and deserialize Avro data\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Get specific patch\n")
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz -patch-key mem_patch_01def\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Interactive browser - navigate patches, see JSON live\n")
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Output all patches as JSON array\n")
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz -all\n\n", os.Args[0])
	}

	flag.Parse()

	if *profileID == "" || *storeID == "" {
		fmt.Fprintln(os.Stderr, "Error: -profile-id and -store-id are required")
		flag.Usage()
		os.Exit(1)
	}

	// Determine which profile we're using
	activeProfile := *awsProfile
	if activeProfile == "" {
		activeProfile = os.Getenv("AWS_PROFILE")
	}
	if activeProfile == "" {
		activeProfile = "default"
	}

	if *awsProfile != "" {
		os.Setenv("AWS_PROFILE", *awsProfile)
	}

	tableName := buildTableName(*env, *region, *cell)
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(*region))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", formatAWSError(err, activeProfile))
		os.Exit(1)
	}

	// Validate AWS credentials before proceeding
	fmt.Fprintf(os.Stderr, "Validating AWS credentials (profile: %s)...\n", activeProfile)
	if err := validateAWSCredentials(ctx, cfg, activeProfile); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Credentials valid.\n\n")

	client := dynamodb.NewFromConfig(cfg)

	fmt.Fprintf(os.Stderr, "Table: %s\n", tableName)
	fmt.Fprintf(os.Stderr, "Profile ID: %s\n", *profileID)
	fmt.Fprintf(os.Stderr, "Store ID: %s\n", *storeID)
	if *patchKey != "" {
		fmt.Fprintf(os.Stderr, "Patch Key: %s\n", *patchKey)
	} else {
		fmt.Fprintf(os.Stderr, "Patch Key: (all)\n")
	}
	fmt.Fprintf(os.Stderr, "Querying with N from 0 to %d...\n", maxN)

	results, err := findPatches(ctx, client, tableName, *profileID, *storeID, *patchKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "No matching patches found")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Found %d patch(es)\n\n", len(results))

	// Single result or specific patch key - output directly
	if len(results) == 1 || *patchKey != "" {
		outputResult(results[0], *raw)
		return
	}

	// Multiple results with -all flag
	if *all {
		outputAllResults(results, *raw)
		return
	}

	// Interactive TUI
	m := model{
		results:    results,
		cursor:     0,
		viewHeight: min(10, len(results)),
	}
	m.updateJSON()

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	// If user pressed Enter, output the selected patch
	fm := finalModel.(model)
	if fm.cursor >= 0 && fm.cursor < len(fm.results) {
		// Check if they quit with 'q' or selected with Enter
		// We output the selected item
		outputResult(fm.results[fm.cursor], *raw)
	}
}

func buildTableName(env, region, cell string) string {
	return fmt.Sprintf("%s-%s-%s.IdentityPatch.v1", env, region, cell)
}

func findPatches(ctx context.Context, client *dynamodb.Client, tableName, profileID, storeID, patchKey string) ([]*Result, error) {
	jobs := make(chan QueryJob, (maxN + 1))
	resultsChan := make(chan *Result, (maxN+1)*10)
	errChan := make(chan error, concurrency)

	var wg sync.WaitGroup
	var errOnce sync.Once // Only capture first error

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				queryResults, err := queryPK(ctx, client, tableName, job, patchKey)
				if err != nil {
					errOnce.Do(func() {
						errChan <- err
					})
					continue
				}
				for _, r := range queryResults {
					resultsChan <- r
				}
			}
		}()
	}

	go func() {
		for n := 0; n <= maxN; n++ {
			pk := fmt.Sprintf("%d%s#%s", n, profileID, storeID)
			jobs <- QueryJob{N: n, PK: pk}
		}
		close(jobs)
	}()

	go func() {
		wg.Wait()
		close(resultsChan)
		close(errChan)
	}()

	var results []*Result
	for r := range resultsChan {
		results = append(results, r)
	}

	// Check for errors (non-blocking since channel might be empty)
	select {
	case err := <-errChan:
		if err != nil && len(results) == 0 {
			return nil, err
		}
	default:
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Patch != nil && results[j].Patch != nil {
			return results[i].Patch.AcceptedAt > results[j].Patch.AcceptedAt
		}
		return results[i].SK > results[j].SK
	})

	return results, nil
}

func queryPK(ctx context.Context, client *dynamodb.Client, tableName string, job QueryJob, patchKey string) ([]*Result, error) {
	var input *dynamodb.QueryInput

	if patchKey != "" {
		input = &dynamodb.QueryInput{
			TableName:              aws.String(tableName),
			KeyConditionExpression: aws.String("PK = :pk AND SK = :sk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: job.PK},
				":sk": &types.AttributeValueMemberS{Value: patchKey},
			},
		}
	} else {
		input = &dynamodb.QueryInput{
			TableName:              aws.String(tableName),
			KeyConditionExpression: aws.String("PK = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: job.PK},
			},
		}
	}

	output, err := client.Query(ctx, input)
	if err != nil {
		// Check for specific errors that should be reported
		errStr := err.Error()
		if strings.Contains(errStr, "AccessDenied") ||
			strings.Contains(errStr, "ResourceNotFoundException") ||
			strings.Contains(errStr, "UnrecognizedClientException") {
			return nil, errors.New(errStr)
		}
		// Other errors (like throttling) we can ignore for individual queries
		return nil, nil
	}

	if len(output.Items) == 0 {
		return nil, nil
	}

	var results []*Result
	for _, item := range output.Items {
		avroDataAttr, ok := item["AvroData"]
		if !ok {
			continue
		}

		avroDataBinary, ok := avroDataAttr.(*types.AttributeValueMemberB)
		if !ok {
			continue
		}

		sk := ""
		if skAttr, ok := item["SK"].(*types.AttributeValueMemberS); ok {
			sk = skAttr.Value
		}

		result := &Result{
			N:        job.N,
			PK:       job.PK,
			SK:       sk,
			AvroData: avroDataBinary.Value,
		}

		if patch, err := deserializeAvro(avroDataBinary.Value); err == nil {
			result.Patch = patch
		}

		results = append(results, result)
	}

	return results, nil
}

func deserializeAvro(data []byte) (*avro.ProfileIdentifierPatch, error) {
	reader := bytes.NewReader(data)
	patch, err := avro.DeserializeProfileIdentifierPatch(reader)
	if err != nil {
		return nil, err
	}
	return &patch, nil
}

func outputResult(result *Result, raw bool) {
	if raw {
		fmt.Println(base64.StdEncoding.EncodeToString(result.AvroData))
		return
	}

	patch := result.Patch
	if patch == nil {
		var err error
		patch, err = deserializeAvro(result.AvroData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to deserialize Avro: %v\n", err)
			os.Exit(1)
		}
	}

	jsonData, err := json.MarshalIndent(patch, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal to JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(jsonData))
}

func outputAllResults(results []*Result, raw bool) {
	if raw {
		for _, r := range results {
			fmt.Println(base64.StdEncoding.EncodeToString(r.AvroData))
		}
		return
	}

	var patches []*avro.ProfileIdentifierPatch
	for _, r := range results {
		patch := r.Patch
		if patch == nil {
			var err error
			patch, err = deserializeAvro(r.AvroData)
			if err != nil {
				continue
			}
		}
		patches = append(patches, patch)
	}

	jsonData, err := json.MarshalIndent(patches, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal to JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(jsonData))
}
