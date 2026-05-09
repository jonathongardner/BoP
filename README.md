# Bank of Parents (BoP)

A small internal web app for managing **people**, their **accounts**, deposits, and withdrawals. The backend is plain Go; the UI is a single HTML page using Vue 3 and Bulma from CDNs. Data is stored in SQLite ([modernc.org/sqlite](https://modernc.org/sqlite), pure Go, no CGO) in a file you choose—stop the process and copy that file to back it up.

## Requirements

- [Go](https://go.dev/dl/) **1.25** or newer (see `go.mod`).

## Setup

Clone the repo and build from the module root:

```bash
git clone <repository-url>
cd bop
go build -o bop .
```

Or run without installing a binary:

```bash
go run . serve
```

## Running the app

Start the HTTP server (API + web UI):

```bash
./bop serve
```

Defaults:

- Listens on **`127.0.0.1:8080`**
- SQLite database file **`bop.db`** in the current working directory

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--listen` | `-l` | `127.0.0.1:8080` | Address and port for HTTP |
| `--db` | | `bop.db` | Path to the SQLite database file |

Examples:

```bash
# Listen on all interfaces (e.g. LAN)
./bop serve --listen 0.0.0.0:8080

# Put the database in a fixed location (directories are created if needed)
./bop serve --db /var/lib/bop/data.db
```

Open a browser to the listen URL (for defaults: **http://127.0.0.1:8080**).

Other CLI commands:

```bash
./bop --help
./bop serve --help
./bop --version
```

## Using the UI

1. **Add people** — Use **Add person** (bottom-right, next to **Add account**), enter a name, then **Create**.
2. **Add accounts** — **Add account** opens a modal where you **choose the person** and enter the account name. Every account must belong to someone.
3. **View balances** — The home page shows one **card per person**; inside each card, accounts appear as tiles with balances.
4. **Transactions** — Click an account tile to deposit, withdraw, add an optional description (up to 255 characters), or delete transactions / the account (same rules as before: no overdraft on withdraw; delete account only at **$0.00**).

There is no authentication; run only on a network you trust.

### Upgrading from an older database

If you already had a `bop.db` from before **people** existed, the app migrates automatically: it creates a **`Household`** person and attaches existing accounts to that person.

## Backup and persistence

All application state lives in the SQLite file (`--db`). To back up:

1. Stop `serve` (or copy while running only if you accept possible inconsistent reads—clean backup prefers a stopped process).
2. Copy the database file to safe storage.

Restore by pointing `--db` at the copied file or replacing the live file when the app is stopped.

## HTTP API (optional)

Same origin as the UI; examples assume `127.0.0.1:8080`.

| Method | Path | Body | Notes |
|--------|------|------|--------|
| `GET` | `/` | — | Web UI |
| `GET` | `/api/persons` | — | People with nested `accounts` arrays |
| `POST` | `/api/persons` | `{"name":"Ada"}` | Create person; **409** if name already exists |
| `GET` | `/api/accounts` | — | Flat list of accounts (includes `person_id`) |
| `POST` | `/api/accounts` | `{"name":"Savings","person_id":1}` | Create account for that person; **409** if that person already has an account with the same name |
| `GET` | `/api/accounts/{id}/transactions` | — | List transactions |
| `POST` | `/api/accounts/{id}/deposit` | `{"amount_cents":5000,"description":"Allowance"}` | Add funds (`amount_cents` > 0). `description` optional; max **255 characters** (Unicode). |
| `POST` | `/api/accounts/{id}/withdraw` | `{"amount_cents":1000,"description":"Snacks"}` | Remove funds; **400** if insufficient funds or description too long |
| `DELETE` | `/api/accounts/{id}` | — | Delete account only if **balance is zero**; **400** otherwise |
| `DELETE` | `/api/accounts/{id}/transactions/{txid}` | — | Remove a transaction and adjust the account balance; **404** if missing; **400** if the adjustment would violate balance rules |

Amounts are integer **cents** in JSON to avoid floating-point rounding issues.
