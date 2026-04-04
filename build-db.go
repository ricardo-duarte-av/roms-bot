package main

import (
    "bufio"
    "database/sql"
    "fmt"
    "log"
    "net/url"
    "os"
    "strings"

    _ "github.com/mattn/go-sqlite3"
)

func main() {
    infile := "index.txt"
    dbfile := "links.db"

    file, err := os.Open(infile)
    if err != nil {
        log.Fatalf("Could not open %s: %v", infile, err)
    }
    defer file.Close()

    db, err := sql.Open("sqlite3", dbfile)
    if err != nil {
        log.Fatalf("Could not open SQLite db: %v", err)
    }
    defer db.Close()

    // Create table if not exists
    _, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS files (
            section TEXT,
            console TEXT,
            file TEXT,
            rawurl TEXT PRIMARY KEY
        )
    `)
    if err != nil {
        log.Fatalf("Could not create table: %v", err)
    }

    scanner := bufio.NewScanner(file)
    scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

    tx, err := db.Begin()
    if err != nil {
        log.Fatalf("Could not begin transaction: %v", err)
    }
    stmt, err := tx.Prepare("INSERT OR IGNORE INTO files(section, console, file, rawurl) VALUES (?, ?, ?, ?)")
    if err != nil {
        log.Fatalf("Could not prepare insert: %v", err)
    }
    defer stmt.Close()

    const baseURL = "https://minerva-archive.org/"
    // rawurl format: https://minerva-archive.org/rom?name=./Section/Console/File.zip
    count := 0
    for scanner.Scan() {
        line := scanner.Text()
        lower := strings.ToLower(line)

        if strings.Contains(lower, "(cdn)") {
            continue
        }
        if strings.Contains(lower, "(encrypted)") {
            continue
        }
        if strings.Contains(lower, "audio_cd") {
            continue
        }
        if strings.Contains(lower, "audio cd") {
            continue
        }
        if strings.Contains(lower, "(deprecated)") {
            continue
        }

        if !strings.HasPrefix(line, "./") {
            continue
        }
        if !strings.HasSuffix(line, ".zip") {
            continue
        }

        rel := strings.TrimPrefix(line, "./")
        parts := strings.SplitN(rel, "/", 3)
        if len(parts) != 3 {
            continue // skip entries without at least section/console/file
        }

        section := parts[0]
        console := parts[1]
        filepart := parts[2]

        // Build the Minerva URL: ?name=./path with spaces and special chars encoded,
        // but '/' kept literal. url.QueryEscape handles & and other query-breaking
        // chars correctly; we then swap + back to %20 and unescape slashes.
        encoded := url.QueryEscape(line)
        encoded = strings.ReplaceAll(encoded, "+", "%20")
        encoded = strings.ReplaceAll(encoded, "%2F", "/")
        rawurl := baseURL + "rom?name=" + encoded

        _, err = stmt.Exec(section, console, filepart, rawurl)
        if err != nil {
            log.Printf("Failed to insert: %v", err)
        }
        count++
        if count%10000 == 0 {
            fmt.Printf("Inserted %d rows...\n", count)
        }
    }
    if err := scanner.Err(); err != nil {
        log.Fatalf("Scanner error: %v", err)
    }
    err = tx.Commit()
    if err != nil {
        log.Fatalf("Could not commit transaction: %v", err)
    }
    fmt.Printf("Done! Inserted %d rows.\n", count)
}

