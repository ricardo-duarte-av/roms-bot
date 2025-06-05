package main

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "sort"
    "strings"
    "time"

    "gopkg.in/yaml.v3"
    "maunium.net/go/mautrix"
    "maunium.net/go/mautrix/event"
    "maunium.net/go/mautrix/id"
    _ "github.com/mattn/go-sqlite3"
)

type MatrixConfig struct {
    Server   string `yaml:"server"`
    Username string `yaml:"username"`
    Password string `yaml:"password"`
    Room     string `yaml:"room"`
}

type Config struct {
    Matrix MatrixConfig `yaml:"matrix"`
}

type TokenStore struct {
    AccessToken string `json:"access_token"`
    UserID      string `json:"user_id"`
    DeviceID    string `json:"device_id"`
}

func loadConfig(path string) (*Config, error) {
    data, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

func loadToken(path string) (*TokenStore, error) {
    data, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var ts TokenStore
    if err := json.Unmarshal(data, &ts); err != nil {
        return nil, err
    }
    return &ts, nil
}

func saveToken(path string, ts *TokenStore) error {
    data, err := json.MarshalIndent(ts, "", "  ")
    if err != nil {
        return err
    }
    return ioutil.WriteFile(path, data, 0600)
}

func main() {
    startTime := time.Now()
    cfg, err := loadConfig("config.yaml")
    if err != nil {
        log.Fatalf("Failed to load config: %v", err)
    }

    tokenPath := "token.json"
    var client *mautrix.Client
    var tokenStore *TokenStore

    // Try to load token.json for re-use
    if ts, err := loadToken(tokenPath); err == nil && ts.AccessToken != "" {
        userID := strings.TrimSpace(ts.UserID)
        if !strings.HasPrefix(userID, "@") {
            log.Fatalf("UserID does not start with '@': %q", userID)
        }
        log.Printf("Creating client with UserID: %q", userID)
        client, err = mautrix.NewClient(cfg.Matrix.Server, id.UserID(userID), ts.AccessToken)
        if err != nil {
            log.Fatalf("Failed to create Matrix client with stored token: %v", err)
        }
        client.DeviceID = id.DeviceID(ts.DeviceID)
        log.Println("Loaded access token from file.")
    } else {
        // First-time login
        client, err = mautrix.NewClient(cfg.Matrix.Server, "", "")
        if err != nil {
            log.Fatalf("Failed to create Matrix client: %v", err)
        }
        resp, err := client.Login(context.Background(), &mautrix.ReqLogin{
            Type: "m.login.password",
            Identifier: mautrix.UserIdentifier{
                Type: mautrix.IdentifierTypeUser,
                User: cfg.Matrix.Username,
            },
            Password: cfg.Matrix.Password,
        })
        if err != nil {
            log.Fatalf("Failed to login: %v", err)
        }
        tokenStore = &TokenStore{
            AccessToken: resp.AccessToken,
            UserID:      resp.UserID.String(),
            DeviceID:    resp.DeviceID.String(),
        }
        if !strings.HasPrefix(tokenStore.UserID, "@") {
            log.Fatalf("UserID after login does not start with '@': %q", tokenStore.UserID)
        }
        if err := saveToken(tokenPath, tokenStore); err != nil {
            log.Printf("Warning: Could not save access token: %v", err)
        } else {
            log.Println("Saved access token to file.")
        }

        // Re-create client with correct credentials after login
        client, err = mautrix.NewClient(cfg.Matrix.Server, resp.UserID, resp.AccessToken)
        if err != nil {
            log.Fatalf("Failed to create Matrix client after login: %v", err)
        }
        client.DeviceID = resp.DeviceID
    }

    // open sqlite db once and reuse for all queries
    db, err := sql.Open("sqlite3", "./links.db")
    if err != nil {
        log.Fatalf("Failed to open links.db: %v", err)
    }
    defer db.Close()

    syncer := client.Syncer.(*mautrix.DefaultSyncer)
    syncer.OnEventType(event.EventMessage, mautrix.EventHandler(
        func(ctx context.Context, ev *event.Event) {
            if ev.Sender == client.UserID {
                return // Ignore bot's own messages
            }
            if ev.RoomID.String() != cfg.Matrix.Room {
                return // Ignore other rooms
            }
            // Ignore events from before the bot started
            if ev.Timestamp < startTime.UnixMilli() {
                return
            }
            content, ok := ev.Content.Parsed.(*event.MessageEventContent)
            if !ok || content.MsgType != event.MsgText {
                return
            }
            if strings.HasPrefix(content.Body, "!") {
                handleCommand(ctx, client, db, ev.RoomID, content.Body, ev.ID)
            }
        },
    ))

    log.Println("Bot is running!")
    err = client.Sync()
    if err != nil {
        log.Fatalf("Sync() returned error: %v", err)
    }
}

// parseArgs parses quoted, unquoted, and -negated terms
func parseArgs(query string) (positives []string, negatives []string) {
    tokens := []string{}
    curr := strings.Builder{}
    inQuote := false
    quoteChar := byte(0)
    for i := 0; i < len(query); i++ {
        c := query[i]
        if c == '"' || c == '\'' {
            if inQuote && c == quoteChar {
                if curr.Len() > 0 {
                    tokens = append(tokens, curr.String())
                    curr.Reset()
                }
                inQuote = false
            } else if !inQuote {
                inQuote = true
                quoteChar = c
            } else {
                curr.WriteByte(c)
            }
        } else if c == ' ' && !inQuote {
            if curr.Len() > 0 {
                tokens = append(tokens, curr.String())
                curr.Reset()
            }
        } else {
            curr.WriteByte(c)
        }
    }
    if curr.Len() > 0 {
        tokens = append(tokens, curr.String())
    }
    for _, t := range tokens {
        if len(t) == 0 {
            continue
        }
        if t[0] == '-' {
            negatives = append(negatives, t[1:])
        } else {
            positives = append(positives, t)
        }
    }
    return
}

func buildSQLQuery(positives, negatives []string, maxResults int) (string, []interface{}) {
    where := []string{}
    args := []interface{}{}

    // Each positive: must appear in at least one of the fields
    for _, p := range positives {
        w := "(LOWER(section) LIKE ? OR LOWER(console) LIKE ? OR LOWER(file) LIKE ?)"
        val := "%" + strings.ToLower(p) + "%"
        where = append(where, w)
        args = append(args, val, val, val)
    }

    // Each negative: must NOT appear in any of the fields
    for _, n := range negatives {
        w := "(LOWER(section) NOT LIKE ? AND LOWER(console) NOT LIKE ? AND LOWER(file) NOT LIKE ?)"
        val := "%" + strings.ToLower(n) + "%"
        where = append(where, w)
        args = append(args, val, val, val)
    }

    sql := "SELECT section, console, file, rawurl FROM files"
    if len(where) > 0 {
        sql += " WHERE " + strings.Join(where, " AND ")
    }
    sql += " ORDER BY section, console, file LIMIT ?"
    args = append(args, maxResults+1) // +1 for over-limit check
    return sql, args
}

func handleCommand(ctx context.Context, client *mautrix.Client, db *sql.DB, roomID id.RoomID, body string, eventID id.EventID) {
    const maxResults = 1000
    const batchSize = 100

    cmd := strings.Fields(body)
    if len(cmd) == 0 {
        return
    }
    switch cmd[0] {
    case "!roms":
        query := strings.TrimSpace(body[len("!roms"):])
        log.Printf("!roms command: %q", query)

        positives, negatives := parseArgs(query)
        sqlQuery, args := buildSQLQuery(positives, negatives, maxResults)
        rows, err := db.Query(sqlQuery, args...)
        if err != nil {
            client.SendText(ctx, roomID, "Search error: "+err.Error())
            return
        }
        defer rows.Close()

        type resultRow struct {
            Section string
            Console string
            File    string
            Rawurl  string
        }
        var results []resultRow
        for rows.Next() {
            var section, console, file, rawurl string
            if err := rows.Scan(&section, &console, &file, &rawurl); err != nil {
                continue
            }
            results = append(results, resultRow{
                Section: section, Console: console, File: file, Rawurl: rawurl,
            })
        }
        if err := rows.Err(); err != nil {
            client.SendText(ctx, roomID, "Search error: "+err.Error())
            return
        }

        // Sort by Section, then Console, then File
        sort.Slice(results, func(i, j int) bool {
            if results[i].Section != results[j].Section {
                return results[i].Section < results[j].Section
            }
            if results[i].Console != results[j].Console {
                return results[i].Console < results[j].Console
            }
            return results[i].File < results[j].File
        })

        // Too many results: react with ❌️ and notify, including the number of results
        if len(results) > maxResults {
            reactTooMany := map[string]interface{}{
                "m.relates_to": map[string]interface{}{
                    "rel_type": "m.annotation",
                    "event_id": eventID,
                    "key":      "❌️",
                },
            }
            _, _ = client.SendMessageEvent(ctx, roomID, event.EventReaction, reactTooMany)
            tooManyMsg := map[string]interface{}{
                "msgtype": "m.text",
                "body":    fmt.Sprintf("Too many results: %d", len(results)),
                "m.relates_to": map[string]interface{}{
                    "m.in_reply_to": map[string]interface{}{
                        "event_id": eventID,
                    },
                },
            }
            _, _ = client.SendMessageEvent(ctx, roomID, event.EventMessage, tooManyMsg)
            return
        }

        // React with ✅️ to confirm
        reactOk := map[string]interface{}{
            "m.relates_to": map[string]interface{}{
                "rel_type": "m.annotation",
                "event_id": eventID,
                "key":      "✅️",
            },
        }
        _, _ = client.SendMessageEvent(ctx, roomID, event.EventReaction, reactOk)

        // Threading logic
        previousMsgID := eventID // Start with the user's message as the thread root

        for batchStart := 0; batchStart < len(results); batchStart += batchSize {
            batchEnd := batchStart + batchSize
            if batchEnd > len(results) {
                batchEnd = len(results)
            }
            batch := results[batchStart:batchEnd]

            var html strings.Builder
            var plain strings.Builder
            for _, row := range batch {
                plain.WriteString(fmt.Sprintf("%s - %s - %s\n", row.Section, row.Console, row.File))
                html.WriteString(fmt.Sprintf(
                    "%s - %s - <a href=\"%s\">%s</a><br>",
                    htmlEscape(row.Section), htmlEscape(row.Console), row.Rawurl, htmlEscape(row.File),
                ))
            }

            messageContent := map[string]interface{}{
                "msgtype":        "m.text",
                "body":           plain.String(),
                "format":         "org.matrix.custom.html",
                "formatted_body": html.String(),
                "m.relates_to": map[string]interface{}{
                    "event_id":        eventID, // always the thread root (user message)
                    "is_falling_back": true,
                    "m.in_reply_to": map[string]interface{}{
                        "event_id": previousMsgID, // previous message or thread root
                    },
                    "rel_type": "m.thread",
                },
            }
            resp, err := client.SendMessageEvent(ctx, roomID, event.EventMessage, messageContent)
            if err != nil {
                log.Printf("Failed to send HTML message: %v", err)
                break
            }
            previousMsgID = resp.EventID // For next batch, reply to our last message
        }
    }
}

func htmlEscape(s string) string {
    replacer := strings.NewReplacer(
        "&", "&amp;",
        "<", "&lt;",
        ">", "&gt;",
        "\"", "&quot;",
        "'", "&#39;",
    )
    return replacer.Replace(s)
}

