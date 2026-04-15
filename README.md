# patch-reader

CLI tool to query DynamoDB IdentityPatch table and deserialize Avro data to JSON.

## Installation

### Download from Releases

Download the appropriate binary for your platform from the [Releases](https://github.com/kaysush-twilio/patch-reader/releases) page.

```bash
# macOS (Apple Silicon)
curl -L https://github.com/kaysush-twilio/patch-reader/releases/latest/download/patch-reader-darwin-arm64 -o patch-reader
chmod +x patch-reader
sudo mv patch-reader /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/kaysush-twilio/patch-reader/releases/latest/download/patch-reader-darwin-amd64 -o patch-reader
chmod +x patch-reader
sudo mv patch-reader /usr/local/bin/

# Linux (x86_64)
curl -L https://github.com/kaysush-twilio/patch-reader/releases/latest/download/patch-reader-linux-amd64 -o patch-reader
chmod +x patch-reader
sudo mv patch-reader /usr/local/bin/
```

### Build from source

```bash
git clone https://github.com/kaysush-twilio/patch-reader.git
cd patch-reader
make build
```

## Usage

```bash
patch-reader -profile-id <PROFILE_ID> -store-id <STORE_ID> [options]
```

### Required flags

- `-profile-id`: Profile ID (e.g., `mem_profile_01abc123`)
- `-store-id`: Store ID (e.g., `mem_store_01xyz789`)

### Optional flags

- `-patch-key`: Patch Key / SK - if omitted, shows all patches with interactive selector
- `-env`: Environment - `dev`, `stage`, or `prod` (default: `dev`)
- `-region`: AWS region (default: `us-east-1`)
- `-cell`: Cell identifier (default: `cell-1`)
- `-aws-profile`: AWS profile to use (overrides `AWS_PROFILE` env var)
- `-all`: Output all matches as JSON array (skip interactive selection)
- `-raw`: Output raw Avro bytes (base64) instead of JSON

### Examples

```bash
# Get a specific patch by key
patch-reader -profile-id mem_profile_01kp6w3xvgfphr6m5hbd7fdx9n \
             -store-id mem_store_01khtdvhg1fxftkhazx806n84h \
             -patch-key mem_patch_01kp6w4xcaf6782ffz83cwg6ew

# List all patches for a profile (interactive selector)
# Use arrow keys to navigate, type to search, enter to select
patch-reader -profile-id mem_profile_01kp6w3xvgfphr6m5hbd7fdx9n \
             -store-id mem_store_01khtdvhg1fxftkhazx806n84h

# Output all patches as JSON array (no interactive selection)
patch-reader -profile-id mem_profile_01kp6w3xvgfphr6m5hbd7fdx9n \
             -store-id mem_store_01khtdvhg1fxftkhazx806n84h \
             -all

# Specify AWS profile
patch-reader -profile-id mem_profile_01abc \
             -store-id mem_store_01xyz \
             -aws-profile memora-dev-admin

# Query production environment
patch-reader -profile-id mem_profile_01abc \
             -store-id mem_store_01xyz \
             -env prod \
             -aws-profile memora-prod-admin

# Output raw Avro (base64)
patch-reader -profile-id mem_profile_01abc \
             -store-id mem_store_01xyz \
             -patch-key mem_patch_01def \
             -raw
```

### Interactive Selector

When `-patch-key` is omitted and multiple patches are found, an interactive selector is displayed:

```
Select a patch (use arrow keys, type to search, enter to select):
👉 mem_patch_01kp6w5bksfj482tjgnvashxwd | 2026-04-15 09:30:39 | identifier.unchanged | NORMAL
   mem_patch_01kp6w4xcaf6782ffz83cwg6ew | 2026-04-15 09:30:24 | identifier.unchanged | NORMAL
   mem_patch_01kp6w3yc8f359gh914knyb3dg | 2026-04-15 09:29:53 | profile.merged | NORMAL
```

- **Arrow keys**: Navigate up/down
- **Type**: Filter/search patches
- **Enter**: Select and output the patch
- **Ctrl+C**: Cancel

### Shell alias (optional)

Add to your `~/.zshrc` or `~/.bashrc`:

```bash
alias patch-reader='AWS_PROFILE=memora-dev-admin patch-reader'
```

## How it works

The tool queries DynamoDB with partition keys in the format `{N}{profileID}#{storeID}` where N ranges from 0 to 1000. It uses 100 concurrent goroutines to query in parallel, making it fast even with 1001 potential partition keys to check.

When a patch key is provided, it queries with both PK and SK for exact match. When omitted, it retrieves all patches for the profile and presents them in an interactive selector sorted by timestamp (newest first).

Once selected, it deserializes the Avro data stored in the `AvroData` attribute and outputs it as formatted JSON.

## License

Internal Twilio tool.
