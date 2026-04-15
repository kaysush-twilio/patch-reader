# patch-reader

CLI tool to query DynamoDB IdentityPatch table and deserialize Avro data to JSON.

## Installation

### Quick Install (Recommended)

**From release (no Go required):**
```bash
curl -fsSL https://raw.githubusercontent.com/kaysush-twilio/patch-reader/main/install.sh | bash
```

**From source:**
```bash
git clone https://github.com/kaysush-twilio/patch-reader.git
cd patch-reader
./install.sh
```

The installer will:
- Download/build the binary for your platform
- Install to `/usr/local/bin`
- Optionally set up a `pr` alias with your AWS profile

### Manual Installation

**Download binary:**
```bash
# macOS (Apple Silicon)
curl -L https://github.com/kaysush-twilio/patch-reader/releases/latest/download/patch-reader-darwin-arm64 -o patch-reader

# macOS (Intel)
curl -L https://github.com/kaysush-twilio/patch-reader/releases/latest/download/patch-reader-darwin-amd64 -o patch-reader

# Linux (x86_64)
curl -L https://github.com/kaysush-twilio/patch-reader/releases/latest/download/patch-reader-linux-amd64 -o patch-reader

# Install
chmod +x patch-reader
sudo mv patch-reader /usr/local/bin/
```

**Build from source:**
```bash
git clone https://github.com/kaysush-twilio/patch-reader.git
cd patch-reader
make build
sudo cp build/patch-reader /usr/local/bin/
```

## AWS Setup

This tool requires AWS credentials with access to the IdentityPatch DynamoDB tables.

### 1. Configure AWS SSO Profile

Add to `~/.aws/config`:

```ini
[sso-session twilio]
sso_start_url = https://twilio-us-east-1.awsapps.com/start
sso_region = us-east-1
sso_registration_scopes = sso:account:access

# Dev environment
[profile memora-dev-admin]
sso_session = twilio
sso_account_id = <DEV_ACCOUNT_ID>
sso_role_name = Standard_PowerUser
region = us-east-1

# Stage environment (update account ID)
[profile memora-stage-admin]
sso_session = twilio
sso_account_id = <STAGE_ACCOUNT_ID>
sso_role_name = Standard_PowerUser
region = us-east-1

# Prod environment (update account ID)
[profile memora-prod-admin]
sso_session = twilio
sso_account_id = <PROD_ACCOUNT_ID>
sso_role_name = Standard_PowerUser
region = us-east-1
```

### 2. Login via SSO

```bash
aws sso login --sso-session twilio
```

### 3. (Optional) Create Shell Alias

Add to `~/.zshrc` or `~/.bashrc`:

```bash
alias pr='AWS_PROFILE=memora-dev-admin patch-reader'
```

Then reload: `source ~/.zshrc`

## Usage

```bash
patch-reader -profile-id <PROFILE_ID> -store-id <STORE_ID> [options]
```

### Required Flags

| Flag | Description |
|------|-------------|
| `-profile-id` | Profile ID (e.g., `mem_profile_01abc123`) |
| `-store-id` | Store ID (e.g., `mem_store_01xyz789`) |

### Optional Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-patch-key` | (all) | Specific patch key; if omitted, shows all patches |
| `-env` | `dev` | Environment: `dev`, `stage`, `prod` |
| `-region` | `us-east-1` | AWS region |
| `-cell` | `cell-1` | Cell identifier |
| `-aws-profile` | | AWS profile (overrides `AWS_PROFILE` env var) |
| `-all` | `false` | Output all matches as JSON array |
| `-raw` | `false` | Output raw Avro bytes (base64) |

### Examples

```bash
# Interactive browser - navigate patches, see JSON live
patch-reader -profile-id mem_profile_01abc -store-id mem_store_01xyz \
    -aws-profile memora-dev-admin

# Get a specific patch
patch-reader -profile-id mem_profile_01abc -store-id mem_store_01xyz \
    -patch-key mem_patch_01def -aws-profile memora-dev-admin

# Output all patches as JSON array
patch-reader -profile-id mem_profile_01abc -store-id mem_store_01xyz \
    -all -aws-profile memora-dev-admin

# Query production
patch-reader -profile-id mem_profile_01abc -store-id mem_store_01xyz \
    -env prod -aws-profile memora-prod-admin

# Using the 'pr' alias (if configured)
pr -profile-id mem_profile_01abc -store-id mem_store_01xyz
```

## Interactive Mode

When `-patch-key` is omitted and multiple patches exist, an interactive TUI is displayed:

```
Patches (3 found) - Navigate: ↑/↓  Scroll JSON: PgUp/PgDn  Select: Enter  Quit: q/Ctrl+C

▸ mem_patch_01kp6w5bks... │ 2026-04-15 09:30:39 │ identifier.unchanged │ NORMAL
  mem_patch_01kp6w4xca... │ 2026-04-15 09:30:24 │ identifier.unchanged │ NORMAL
  mem_patch_01kp6w3yc8... │ 2026-04-15 09:29:53 │ profile.merged       │ NORMAL

─── JSON Preview ───────────────────────────────────────────────────────────────
{
  "acceptedAt": 1776199839353,
  "accountId": "ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "event": {
    "name": "identifier.unchanged",
    ...
  }
}
```

### Keyboard Controls

| Key | Action |
|-----|--------|
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `PgUp` | Scroll JSON up |
| `PgDn` | Scroll JSON down |
| `Home` | Jump to first patch |
| `End` | Jump to last patch |
| `Enter` | Select patch and output JSON to stdout |
| `q` / `Ctrl+C` | Quit |

## How It Works

The tool queries DynamoDB with partition keys in the format `{N}{profileID}#{storeID}` where N ranges from 0 to 1000. It uses 100 concurrent goroutines for fast parallel queries.

When a patch key is provided, it queries with both PK and SK for an exact match. When omitted, it retrieves all patches and presents them in an interactive TUI sorted by timestamp (newest first).

The Avro data stored in the `AvroData` attribute is deserialized and displayed as formatted JSON.

## Troubleshooting

**"No matching patches found"**
- Verify the profile ID and store ID are correct
- Check you're querying the right environment (`-env`)
- Ensure your AWS credentials have access to the table

**"Token has expired"**
- Run `aws sso login --sso-session twilio` to refresh credentials

**"ResourceNotFoundException"**
- The table doesn't exist in that environment/region
- Check `-env`, `-region`, and `-cell` flags

## License

Internal Twilio tool.
