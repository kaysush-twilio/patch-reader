package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/kaysush-twilio/patch-reader/internal/avro"
	"github.com/manifoldco/promptui"
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
	Patch    *avro.ProfileIdentifierPatch // Deserialized for display
}

type QueryJob struct {
	N  int
	PK string
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
		fmt.Fprintf(os.Stderr, "  # List all patches for a profile (interactive selector)\n")
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Output all patches as JSON array\n")
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz -all\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # With AWS profile\n")
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz -aws-profile memora-dev-admin\n", os.Args[0])
	}

	flag.Parse()

	// Validate required flags
	if *profileID == "" || *storeID == "" {
		fmt.Fprintln(os.Stderr, "Error: -profile-id and -store-id are required")
		flag.Usage()
		os.Exit(1)
	}

	// Set AWS_PROFILE if specified
	if *awsProfile != "" {
		os.Setenv("AWS_PROFILE", *awsProfile)
	}

	// Build table name based on environment
	tableName := buildTableName(*env, *region, *cell)

	ctx := context.Background()

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(*region))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load AWS config: %v\n", err)
		os.Exit(1)
	}

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

	fmt.Fprintf(os.Stderr, "\nFound %d patch(es)\n\n", len(results))

	// Single result - output directly
	if len(results) == 1 {
		outputResult(results[0], *raw)
		return
	}

	// Multiple results
	if *all {
		// Output all as JSON array
		outputAllResults(results, *raw)
		return
	}

	// Interactive selection
	selected := interactiveSelect(results)
	if selected != nil {
		outputResult(selected, *raw)
	}
}

func buildTableName(env, region, cell string) string {
	return fmt.Sprintf("%s-%s-%s.IdentityPatch.v1", env, region, cell)
}

func findPatches(ctx context.Context, client *dynamodb.Client, tableName, profileID, storeID, patchKey string) ([]*Result, error) {
	jobs := make(chan QueryJob, (maxN + 1))
	resultsChan := make(chan *Result, (maxN+1)*10) // Buffer for multiple results per PK

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				queryResults := queryPK(ctx, client, tableName, job, patchKey)
				for _, r := range queryResults {
					resultsChan <- r
				}
			}
		}()
	}

	// Send jobs
	go func() {
		for n := 0; n <= maxN; n++ {
			pk := fmt.Sprintf("%d%s#%s", n, profileID, storeID)
			jobs <- QueryJob{N: n, PK: pk}
		}
		close(jobs)
	}()

	// Wait for workers and close results
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect all results
	var results []*Result
	for r := range resultsChan {
		results = append(results, r)
	}

	// Sort by acceptedAt timestamp (newest first)
	sort.Slice(results, func(i, j int) bool {
		if results[i].Patch != nil && results[j].Patch != nil {
			return results[i].Patch.AcceptedAt > results[j].Patch.AcceptedAt
		}
		return results[i].SK > results[j].SK
	})

	return results, nil
}

func queryPK(ctx context.Context, client *dynamodb.Client, tableName string, job QueryJob, patchKey string) []*Result {
	var input *dynamodb.QueryInput

	if patchKey != "" {
		// Query with specific SK
		input = &dynamodb.QueryInput{
			TableName:              aws.String(tableName),
			KeyConditionExpression: aws.String("PK = :pk AND SK = :sk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: job.PK},
				":sk": &types.AttributeValueMemberS{Value: patchKey},
			},
		}
	} else {
		// Query all items for this PK
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
		fmt.Fprintf(os.Stderr, "[N=%d] ERROR: %v\n", job.N, err)
		return nil
	}

	if len(output.Items) == 0 {
		return nil
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

		// Pre-deserialize for display purposes
		if patch, err := deserializeAvro(avroDataBinary.Value); err == nil {
			result.Patch = patch
		}

		results = append(results, result)
	}

	return results
}

func deserializeAvro(data []byte) (*avro.ProfileIdentifierPatch, error) {
	reader := bytes.NewReader(data)
	patch, err := avro.DeserializeProfileIdentifierPatch(reader)
	if err != nil {
		return nil, err
	}
	return &patch, nil
}

func formatTimestamp(ts int64) string {
	if ts == 0 {
		return "N/A"
	}
	t := time.UnixMilli(ts)
	return t.Format("2006-01-02 15:04:05")
}

func interactiveSelect(results []*Result) *Result {
	// Build display items
	type displayItem struct {
		Label  string
		Result *Result
	}

	items := make([]displayItem, len(results))
	for i, r := range results {
		label := r.SK
		if r.Patch != nil {
			ts := formatTimestamp(r.Patch.AcceptedAt)
			event := r.Patch.Event.Name
			if event == "" {
				event = "unknown"
			}
			summary := string(r.Patch.Summary)
			label = fmt.Sprintf("%s | %s | %s | %s", r.SK, ts, event, summary)
		}
		items[i] = displayItem{Label: label, Result: r}
	}

	templates := &promptui.SelectTemplates{
		Label:    "{{ . }}",
		Active:   "\U0001F449 {{ .Label | cyan }}",
		Inactive: "   {{ .Label | white }}",
		Selected: "\U00002705 {{ .Label | green }}",
		Details: `
--------- Patch Details ----------
{{ "SK:" | faint }}	{{ .Result.SK }}
{{ "PK:" | faint }}	{{ .Result.PK }}
{{- if .Result.Patch }}
{{ "Event:" | faint }}	{{ .Result.Patch.Event.Name }}
{{ "Summary:" | faint }}	{{ .Result.Patch.Summary }}
{{ "AcceptedAt:" | faint }}	{{ .Result.Patch.AcceptedAt }}
{{ "Identifiers:" | faint }}	{{ len .Result.Patch.Identifiers }}
{{ "Merges:" | faint }}	{{ len .Result.Patch.Merges }}
{{- end }}`,
	}

	searcher := func(input string, index int) bool {
		item := items[index]
		return contains(item.Label, input)
	}

	prompt := promptui.Select{
		Label:     "Select a patch (use arrow keys, type to search, enter to select):",
		Items:     items,
		Templates: templates,
		Size:      15,
		Searcher:  searcher,
	}

	idx, _, err := prompt.Run()
	if err != nil {
		if err == promptui.ErrInterrupt {
			fmt.Fprintln(os.Stderr, "Selection cancelled")
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Selection failed: %v\n", err)
		os.Exit(1)
	}

	return items[idx].Result
}

func contains(s, substr string) bool {
	return bytes.Contains(bytes.ToLower([]byte(s)), bytes.ToLower([]byte(substr)))
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
		// Output each base64 on its own line
		for _, r := range results {
			fmt.Println(base64.StdEncoding.EncodeToString(r.AvroData))
		}
		return
	}

	// Collect all patches
	var patches []*avro.ProfileIdentifierPatch
	for _, r := range results {
		patch := r.Patch
		if patch == nil {
			var err error
			patch, err = deserializeAvro(r.AvroData)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to deserialize patch %s: %v\n", r.SK, err)
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
