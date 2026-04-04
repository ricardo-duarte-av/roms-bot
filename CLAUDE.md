# roms-bot

A Matrix bot that lets users search a ROM archive and get direct download links. It is backed by a SQLite database built from Project Minerva's file index.

---

## Architecture overview

The project has two independent binaries that share the same `package main` declaration and are compiled separately:

| File | Binary | Purpose |
|---|---|---|
| `build-db.go` | `build-db` | One-shot tool: parses `index.txt` and populates `links.db` |
| `main.go` | `roms-bot` | Long-running Matrix bot that queries `links.db` |

---

## Step 1 — Build the database (`build-db.go`)

### Input

`index.txt` — the file index from Project Minerva. Each line is a relative path:

```
./No-Intro/Nintendo - Game Boy/Tetris (World) (Rev 1).zip
./Redump/Sony - PlayStation/Gran Turismo (Europe).zip
```

The file has ~3 million lines. The scanner buffer is set to 1 MB to handle long paths.

### Filtering

Lines are skipped if they:
- Contain `(cdn)`, `(encrypted)`, or `(deprecated)` (case-insensitive)
- Contain `audio_cd` or `audio cd` (case-insensitive)
- Do not start with `./`
- Do not end with `.zip`
- Have fewer than 3 path components (i.e. no `section/console/file`)

### Parsing

After stripping the `./` prefix, each line is split into at most 3 parts on `/`:

```
Section   →  "No-Intro"
Console   →  "Nintendo - Game Boy"
File      →  "Tetris (World) (Rev 1).zip"   (may include subdirectories)
```

These three values are stored as plain decoded text in the DB.

### URL construction

Minerva's download endpoint expects:

```
https://minerva-archive.org/rom?name=./Section/Console/File.zip
```

The `name` value is the original line (`./...`) with percent-encoding applied:
- `url.QueryEscape` is used first (correctly encodes `&`, `=`, `?`, `'`, spaces, etc.)
- `+` is replaced with `%20` (servers expect `%20`, not `+`)
- `%2F` is replaced back with `/` (slashes must remain literal path separators)

This encoded string is stored as `rawurl` in the DB and used directly as an `href` in Matrix messages.

### Output

SQLite database `links.db` with a single table:

```sql
CREATE TABLE files (
    section TEXT,
    console TEXT,
    file    TEXT,
    rawurl  TEXT PRIMARY KEY
)
```

`INSERT OR IGNORE` is used so re-running the tool on an updated `index.txt` is safe — existing rows are kept and only new ones are added.

Progress is printed every 10,000 inserted rows.

### Building and running

```bash
go build -o build-db build-db.go
./build-db
```

---

## Step 2 — Run the bot (`main.go`)

### Configuration

Copy `sample.config.yaml` to `config.yaml` and fill in the Matrix credentials:

```yaml
matrix:
  server: "https://matrix.org"
  username: "@botname:matrix.org"
  password: "yourpassword"
  room: "!roomid:matrix.org"
```

### Authentication / token persistence

On first run the bot logs in with username+password and saves the access token to `token.json`. On subsequent runs it loads the token directly, avoiding repeated logins. If the token is invalid or missing, it falls back to a fresh login.

### Bot commands

#### `!help`

Reacts to the message with ℹ️ and replies with usage instructions.

#### `!roms <query>`

Searches the database and returns matching download links as a threaded reply.

**Query syntax:**

| Syntax | Meaning |
|---|---|
| `word` | Must appear in section, console, or filename |
| `"multi word"` | Exact phrase match |
| `@console` | Restrict results to this console (partial match) |
| `-word` | Exclude results containing this word |

Examples:
```
!roms mario @nintendo -sports
!roms zelda @"Nintendo 3DS" -digital
```

### Search logic

`buildSQLQuery` translates the parsed arguments into a single SQL query against `links.db`:

- Each positive term → `LOWER(section) LIKE ? OR LOWER(console) LIKE ? OR LOWER(file) LIKE ?`
- Each negative term → `NOT LIKE` on all three columns
- `@console` → `LOWER(console) LIKE ?`
- Results are limited to 1000 rows (+ 1 to detect overflow)

### Result handling

- **0 results** — reacts with ❌️, sends "No results"
- **> 1000 results** — reacts with ❌️, runs a `COUNT(*)` query and reports the exact total
- **1–1000 results** — reacts with ✅️, sends results in batches of 100 as a Matrix thread

Each result is rendered as HTML:

```
1. No-Intro | Nintendo - Game Boy
    Tetris (World) (Rev 1).zip   ← hyperlinked to the Minerva URL
```

Batches are chained as replies within a thread (each batch replies to the previous one).

### Building and running

```bash
go build -o roms-bot main.go
./roms-bot
```

---

## File reference

| File | Description |
|---|---|
| `build-db.go` | DB builder binary |
| `main.go` | Matrix bot binary |
| `index.txt` | Minerva file index (input for build-db) |
| `links.db` | SQLite database (output of build-db, input for bot) |
| `config.yaml` | Matrix credentials (not committed) |
| `sample.config.yaml` | Config template |
| `token.json` | Saved Matrix access token (auto-generated) |
| `go.mod` / `go.sum` | Go module files |
