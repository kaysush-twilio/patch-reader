package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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
}

type QueryJob struct {
	N  int
	PK string
}

func main() {
	// CLI flags
	profileID := flag.String("profile-id", "", "Profile ID (required)")
	storeID := flag.String("store-id", "", "Store ID (required)")
	patchKey := flag.String("patch-key", "", "Patch Key / SK (required)")
	env := flag.String("env", "dev", "Environment: dev, stage, prod")
	region := flag.String("region", "us-east-1", "AWS region")
	cell := flag.String("cell", "cell-1", "Cell identifier")
	raw := flag.Bool("raw", false, "Output raw Avro bytes (base64) instead of JSON")
	awsProfile := flag.String("aws-profile", "", "AWS profile to use (overrides AWS_PROFILE env var)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "patch-reader - Query DynamoDB IdentityPatch table and deserialize Avro data\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz -patch-key mem_patch_01def\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz -patch-key mem_patch_01def -env prod\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -profile-id mem_profile_01abc -store-id mem_store_01xyz -patch-key mem_patch_01def -aws-profile memora-dev-admin\n", os.Args[0])
	}

	flag.Parse()

	// Validate required flags
	if *profileID == "" || *storeID == "" || *patchKey == "" {
		fmt.Fprintln(os.Stderr, "Error: -profile-id, -store-id, and -patch-key are required")
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
	fmt.Fprintf(os.Stderr, "Patch Key: %s\n", *patchKey)
	fmt.Fprintf(os.Stderr, "Querying with N from 0 to %d...\n", maxN)

	result, err := findPatch(ctx, client, tableName, *profileID, *storeID, *patchKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if result == nil {
		fmt.Fprintln(os.Stderr, "No matching patch found")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\nFound at N=%d, PK=%s\n\n", result.N, result.PK)

	if *raw {
		// Output raw base64 encoded Avro
		fmt.Println(base64.StdEncoding.EncodeToString(result.AvroData))
		return
	}

	// Deserialize and output JSON
	patch, err := deserializeAvro(result.AvroData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to deserialize Avro: %v\n", err)
		os.Exit(1)
	}

	jsonData, err := json.MarshalIndent(patch, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal to JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(jsonData))
}

func buildTableName(env, region, cell string) string {
	return fmt.Sprintf("%s-%s-%s.IdentityPatch.v1", env, region, cell)
}

func findPatch(ctx context.Context, client *dynamodb.Client, tableName, profileID, storeID, patchKey string) (*Result, error) {
	jobs := make(chan QueryJob, (maxN + 1))
	results := make(chan *Result, (maxN + 1))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				result := queryPK(ctx, client, tableName, job, patchKey)
				if result != nil {
					results <- result
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
		close(results)
	}()

	// Return first result found
	for r := range results {
		return r, nil
	}

	return nil, nil
}

func queryPK(ctx context.Context, client *dynamodb.Client, tableName string, job QueryJob, patchKey string) *Result {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		KeyConditionExpression: aws.String("PK = :pk AND SK = :sk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: job.PK},
			":sk": &types.AttributeValueMemberS{Value: patchKey},
		},
	}

	output, err := client.Query(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[N=%d] ERROR: %v\n", job.N, err)
		return nil
	}

	if len(output.Items) == 0 {
		return nil
	}

	// Extract AvroData
	item := output.Items[0]
	avroDataAttr, ok := item["AvroData"]
	if !ok {
		fmt.Fprintf(os.Stderr, "[N=%d] Warning: No AvroData attribute found\n", job.N)
		return nil
	}

	avroDataBinary, ok := avroDataAttr.(*types.AttributeValueMemberB)
	if !ok {
		fmt.Fprintf(os.Stderr, "[N=%d] Warning: AvroData is not binary type\n", job.N)
		return nil
	}

	sk := ""
	if skAttr, ok := item["SK"].(*types.AttributeValueMemberS); ok {
		sk = skAttr.Value
	}

	return &Result{
		N:        job.N,
		PK:       job.PK,
		SK:       sk,
		AvroData: avroDataBinary.Value,
	}
}

func deserializeAvro(data []byte) (*avro.ProfileIdentifierPatch, error) {
	reader := bytes.NewReader(data)
	patch, err := avro.DeserializeProfileIdentifierPatch(reader)
	if err != nil {
		return nil, err
	}
	return &patch, nil
}
